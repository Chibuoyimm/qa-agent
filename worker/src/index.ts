import { hostname } from 'node:os';
import { resolve } from 'node:path';
import { chromium, type Browser } from 'playwright';
import { parseAllowedOrigins, parseClaim, safePart, validateBaseUrl, type Claim, type ScenarioResult } from './contract.js';
import { errorResult, executeScenario } from './execute.js';

const POLL_MS = 2000;
const HEARTBEAT_MS = 20000;
const RUN_TIMEOUT_MS = 5 * 60 * 1000;

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

const api = new URL(required('QA_API_BASE_URL'));
if (!['http:', 'https:'].includes(api.protocol) || api.username || api.password || api.search || api.hash || api.pathname !== '/') {
  throw new Error('QA_API_BASE_URL must be an HTTP(S) origin');
}
const workerToken = required('QA_WORKER_TOKEN');
const allowedOrigins = parseAllowedOrigins(required('QA_ALLOWED_ORIGINS'));
const artifactRoot = resolve(required('QA_ARTIFACT_DIR'));
const workerId = process.env.QA_WORKER_ID || `${hostname().slice(0, 40)}-${process.pid}`;
const shutdown = new AbortController();
process.once('SIGINT', () => shutdown.abort());
process.once('SIGTERM', () => shutdown.abort());

async function post(path: string, body: unknown, signal?: AbortSignal): Promise<Response> {
  const timeout = AbortSignal.timeout(10000);
  return fetch(new URL(path, api), {
    method: 'POST',
    redirect: 'error',
    headers: { authorization: `Bearer ${workerToken}`, 'content-type': 'application/json' },
    body: JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
  });
}

async function sleep(ms: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) return;
  await new Promise<void>(resolve => {
    const timer = setTimeout(done, ms);
    function done() { clearTimeout(timer); signal.removeEventListener('abort', done); resolve(); }
    signal.addEventListener('abort', done, { once: true });
  });
}

async function claim(): Promise<Claim | undefined> {
  const response = await post('/api/worker/claim', { worker_id: workerId }, shutdown.signal);
  if (response.status === 204) return;
  if (!response.ok) throw new Error('Worker claim failed');
  return parseClaim(await response.json());
}

async function processRun(claimed: Claim): Promise<void> {
  const { run, lease_token: leaseToken } = claimed;
  const runController = new AbortController();
  const runTimeout = AbortSignal.timeout(RUN_TIMEOUT_MS);
  const onStop = () => runController.abort();
  shutdown.signal.addEventListener('abort', onStop, { once: true });
  runTimeout.addEventListener('abort', onStop, { once: true });
  let leaseLost = false;
  let inFlightHeartbeat: Promise<void> | undefined;
  const heartbeat = async () => {
    if (runController.signal.aborted) return;
    try {
      const response = await post(`/api/worker/runs/${encodeURIComponent(run.id)}/heartbeat`, { lease_token: leaseToken }, runController.signal);
      if (!response.ok) throw new Error('Lease unavailable');
    } catch {
      leaseLost = true;
      runController.abort();
    }
  };
  const timer = setInterval(() => {
    if (!inFlightHeartbeat) inFlightHeartbeat = heartbeat().finally(() => { inFlightHeartbeat = undefined; });
  }, HEARTBEAT_MS);
  let browser: Browser | undefined;
  const results: ScenarioResult[] = [];
  try {
    validateBaseUrl(run.base_url, allowedOrigins);
    browser = await chromium.launch({ headless: true });
    for (const scenario of run.scenarios) {
      if (runController.signal.aborted) break;
      results.push(await executeScenario(browser, run, scenario, allowedOrigins, artifactRoot, runController.signal));
    }
  } catch {
    // Browser startup or an invalid run snapshot is an execution error for each pending scenario.
  } finally {
    clearInterval(timer);
    if (inFlightHeartbeat) await inFlightHeartbeat;
    shutdown.signal.removeEventListener('abort', onStop);
    runTimeout.removeEventListener('abort', onStop);
    if (browser) await browser.close().catch(() => {});
  }
  if (leaseLost || shutdown.signal.aborted) return;
  const completed = new Set(results.map(result => result.scenario_id));
  const remainingMessage = runTimeout.aborted ? 'Run exceeded its time limit' : 'Browser could not execute the scenario';
  for (const scenario of run.scenarios) {
    if (!completed.has(scenario.id)) results.push(errorResult(scenario, remainingMessage));
  }
  try {
    const response = await post(`/api/worker/runs/${encodeURIComponent(run.id)}/complete`, { lease_token: leaseToken, results });
    if (response.status === 409) return; // Cancelled or expired between the last heartbeat and completion.
    if (!response.ok) throw new Error('Completion rejected');
    console.log(`Completed run ${safePart(run.id)}: ${results.map(result => result.status).join(', ')}`);
  } catch {
    console.error(`Could not complete run ${safePart(run.id)}; its lease will expire as an error`);
  }
}

async function main(): Promise<void> {
  console.log(`Worker ${workerId} started`);
  while (!shutdown.signal.aborted) {
    try {
      const claimed = await claim();
      if (claimed) await processRun(claimed);
      else await sleep(POLL_MS, shutdown.signal);
    } catch {
      if (!shutdown.signal.aborted) {
        console.error('Worker API unavailable or returned an invalid claim; retrying');
        await sleep(POLL_MS, shutdown.signal);
      }
    }
  }
}

main().catch(() => { console.error('Worker stopped unexpectedly'); process.exitCode = 1; });
