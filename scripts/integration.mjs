// Disposable PostgreSQL + real API + real Chromium. No customer accounts or model calls.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:net';
import { randomBytes } from 'node:crypto';
import { mkdir, readFile, writeFile, open } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const key = `${Date.now()}-${randomBytes(3).toString('hex')}`;
const evidence = resolve(root, 'artifacts', `integration-${key}`);
await mkdir(evidence, { recursive: true });
const container = `qa-agent-proof-${key}`;
const children = [];
const files = [];
const observations = [];
let containerCreated = false;
const manifest = { started_at: new Date().toISOString(), container, processes: [], runs: [], cleanup: false };
const command = (cmd, args, options = {}) => {
  const result = spawnSync(cmd, args, { cwd: root, encoding: 'utf8', ...options });
  if (result.error || result.status !== 0) throw new Error(`${cmd} failed: ${result.stderr || result.error}`);
  return result.stdout.trim();
};
const port = async () => {
  const server = createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const selected = server.address().port; await new Promise(r => server.close(r)); return selected;
};
const start = async (name, cmd, args, env) => {
  const file = await open(resolve(evidence, `${name}.log`), 'w'); files.push(file);
  const process = spawn(cmd, args, { cwd: root, env: { ...globalThis.process.env, ...env }, stdio: ['ignore', file.fd, file.fd] });
  children.push(process); manifest.processes.push({ name, pid: process.pid });
  process.on('error', error => { process.launchError = error; });
  return process;
};
const stop = async process => {
  if (process.exitCode !== null || process.signalCode !== null) return;
  process.kill('SIGTERM');
  const timer = setTimeout(() => process.kill('SIGKILL'), 5000);
  try { await once(process, 'exit'); } finally { clearTimeout(timer); }
};
const ready = async (url, process) => {
  for (let i = 0; i < 100; i++) {
    if (process.launchError || process.exitCode !== null) throw new Error(`Service exited before readiness: ${url}`);
    try { if ((await fetch(url, { signal: AbortSignal.timeout(500) })).ok) return; } catch {}
    await new Promise(r => setTimeout(r, 100));
  }
  throw new Error(`Service did not become ready: ${url}`);
};

try {
  command('go', ['build', '-o', resolve(evidence, 'server'), './cmd/server']);
  command('go', ['build', '-o', resolve(evidence, 'qa'), './cmd/qa']);
  command('docker', ['run', '--detach', '--name', container, '-e', 'POSTGRES_USER=qa', '-e', 'POSTGRES_PASSWORD=proof-only', '-e', 'POSTGRES_DB=qa_proof', '-p', '127.0.0.1::5432', 'postgres:17-alpine']);
  containerCreated = true;
  const mapping = command('docker', ['port', container, '5432/tcp']);
  const dbPort = mapping.split(':').at(-1);
  let dbReady = false;
  for (let i = 0; i < 100; i++) {
    const result = spawnSync('docker', ['exec', container, 'pg_isready', '-U', 'qa', '-d', 'qa_proof'], { stdio: 'ignore' });
    if (result.status === 0) { dbReady = true; break; }
    await new Promise(r => setTimeout(r, 200));
  }
  assert.ok(dbReady, 'Disposable PostgreSQL readiness');
  const [apiPort, goodPort, badPort] = await Promise.all([port(), port(), port()]);
  const base = `http://127.0.0.1:${apiPort}`;
  const goodURL = `http://127.0.0.1:${goodPort}`;
  const badURL = `http://127.0.0.1:${badPort}`;
  const apiToken = randomBytes(24).toString('hex'), workerToken = randomBytes(24).toString('hex');
  const env = {
    DATABASE_URL: `postgres://qa:proof-only@127.0.0.1:${dbPort}/qa_proof?sslmode=disable`,
    QA_API_TOKEN: apiToken, QA_WORKER_TOKEN: workerToken, QA_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
    QA_API_BASE_URL: base, QA_ALLOWED_ORIGINS: `${goodURL},${badURL}`, QA_ARTIFACT_DIR: resolve(evidence, 'browser'),
    QA_TEST_EMAIL: 'demo@example.test', QA_TEST_PASSWORD: 'pass1234', QA_WORKER_ID: 'integration-worker',
    QA_OPENAI_MODELS: '', QA_OPENAI_API_KEY: '',
  };
  let server = await start('api', resolve(evidence, 'server'), [], env);
  const good = await start('sample-healthy', process.execPath, ['sample/server.js'], { ...env, PORT: String(goodPort), QA_SAMPLE_DEFECT: '0' });
  const bad = await start('sample-faulty', process.execPath, ['sample/server.js'], { ...env, PORT: String(badPort), QA_SAMPLE_DEFECT: '1' });
  await Promise.all([ready(`${base}/healthz`, server), ready(`${goodURL}/healthz`, good), ready(`${badURL}/healthz`, bad)]);
  let worker = await start('worker', process.execPath, ['--import', './worker/node_modules/tsx/dist/loader.mjs', 'worker/src/index.ts'], env);
  const api = async (path, body, options = {}) => {
    const response = await fetch(`${base}${path}`, {
      method: body === undefined ? 'GET' : 'POST', redirect: 'error',
      headers: { authorization: `Bearer ${options.worker ? workerToken : apiToken}`, 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(15000),
    });
    if (options.status) { assert.equal(response.status, options.status, path); return; }
    if (!response.ok) throw new Error(`${path}: HTTP ${response.status}: ${await response.text()}`);
    if (response.status === 204) return null;
    return response.json();
  };
  const templates = JSON.parse(await readFile(resolve(root, 'sample/scenarios.json'), 'utf8'));
  const setup = async (name, url) => {
    const project = await api('/api/projects', { name, base_url: url });
    const scenarios = [];
    for (const template of templates) scenarios.push(await api(`/api/projects/${project.id}/scenarios`, template));
    return { project, scenarios };
  };
  const healthy = await setup('Healthy correctness proof', goodURL);
  const faulty = await setup('Faulty correctness proof', badURL);
  const waitRun = async id => {
    for (let i = 0; i < 600; i++) {
      const run = await api(`/api/runs/${id}`);
      if (!['queued', 'running'].includes(run.status)) return run;
      if (worker.exitCode !== null) throw new Error('Worker exited while waiting for run');
      await new Promise(r => setTimeout(r, 200));
    }
    throw new Error(`Run ${id} timed out`);
  };
  const runSuite = async (suite, mode, expected) => {
    const queued = await api(`/api/projects/${suite.project.id}/runs`, { scenario_ids: suite.scenarios.map(s => s.id), mode });
    manifest.runs.push(queued.id);
    const run = await waitRun(queued.id);
    assert.equal(run.status, expected, JSON.stringify(run.results));
    assert.equal(run.gate, expected === 'passed' ? 'pass' : mode === 'blocking' ? 'fail' : 'warn');
    assert.equal(run.results.length, suite.scenarios.length);
    await writeFile(resolve(evidence, `${queued.id}.json`), JSON.stringify(run, null, 2));
    observations.push({ name: suite.project.name, mode, status: run.status, gate: run.gate, run_id: run.id });
    return run;
  };
  const healthyRun = await runSuite(healthy, 'blocking', 'passed');
  await runSuite(faulty, 'blocking', 'failed');
  await runSuite(faulty, 'advisory', 'failed');
  const blocked = structuredClone(templates[0]);
  const secretStep = blocked.steps.find(s => s.secret_env);
  assert.ok(secretStep, 'Sample must include a secret reference');
  secretStep.secret_env = 'QA_TEST_INTENTIONALLY_MISSING';
  const blockedScenario = await api(`/api/projects/${healthy.project.id}/scenarios`, { ...blocked, name: 'Missing identity blocks a run' });
  await runSuite({ project: healthy.project, scenarios: [blockedScenario] }, 'blocking', 'blocked');
  const draft = await api(`/api/projects/${healthy.project.id}/scenarios`, { ...templates[0], approved: false });
  await api(`/api/projects/${healthy.project.id}/runs`, { scenario_ids: [draft.id], mode: 'blocking' }, { status: 409 });
  observations.push({ name: 'Unapproved scenario rejected', status: 'passed' });
  await stop(worker);
  const queued = await api(`/api/projects/${healthy.project.id}/runs`, { scenario_ids: [healthy.scenarios[0].id], mode: 'blocking' });
  const claim = await api('/api/worker/claim', { worker_id: 'proof-cancel' }, { worker: true });
  assert.equal(claim.run.id, queued.id);
  const cancelled = await api(`/api/runs/${queued.id}/cancel`, {});
  assert.equal(cancelled.status, 'cancelled'); assert.equal(cancelled.gate, 'fail');
  await api(`/api/worker/runs/${queued.id}/complete`, { lease_token: claim.lease_token, results: [] }, { worker: true, status: 409 });
  observations.push({ name: 'Cancellation rejects late completion', status: 'passed' });
  await stop(server);
  server = await start('api-restarted', resolve(evidence, 'server'), [], env);
  await ready(`${base}/healthz`, server);
  const persisted = await api(`/api/runs/${healthyRun.id}`);
  assert.deepEqual(persisted, healthyRun);
  observations.push({ name: 'Results survive API restart', status: 'passed' });
  // The CLI must observe the same gate policy against the real API.
  worker = await start('worker-cli', process.execPath, ['--import', './worker/node_modules/tsx/dist/loader.mjs', 'worker/src/index.ts'], env);
  const cli = spawn(resolve(evidence, 'qa'), ['run', '--project', faulty.project.id, '--scenarios', faulty.scenarios[0].id, '--mode', 'blocking', '--timeout', '60s'], { cwd: root, env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
  children.push(cli); manifest.processes.push({ name: 'cli', pid: cli.pid });
  let cliOutput = ''; cli.stdout.on('data', d => { cliOutput += d; }); cli.stderr.on('data', d => { cliOutput += d; });
  const [code] = await once(cli, 'exit');
  assert.equal(code, 1, cliOutput);
  await writeFile(resolve(evidence, 'cli.txt'), cliOutput);
  observations.push({ name: 'Pipeline CLI blocks faulty build', status: 'passed' });
  const webPort = await port();
  const webURL = `http://127.0.0.1:${webPort}`;
  const web = await start('web', process.execPath, ['web/node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', String(webPort), '--strictPort', 'web'], env);
  await ready(webURL, web);
  const { chromium } = await import('../worker/node_modules/playwright/index.mjs');
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    page.setDefaultTimeout(15000);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(webURL);
    await page.getByLabel('API token', { exact: true }).fill(apiToken);
    await page.getByRole('button', { name: 'Open workspace' }).click();
    await page.getByRole('button', { name: healthy.project.name, exact: true }).click();
    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('button', { name: /^Scenarios/ }).click();
    await page.getByLabel('Select Missing identity blocks a run for next run', { exact: true }).uncheck();
    await page.getByLabel('Mode', { exact: true }).selectOption('blocking');
    await page.getByRole('button', { name: 'Run checks', exact: true }).click();
    await page.getByText('Every selected scenario completed and passed.', { exact: true }).waitFor({ timeout: 90000 });
    assert.deepEqual(errors, [], 'Web workspace must render without uncaught errors');
    if (await page.getByRole('button', { name: 'Dismiss notice', exact: true }).isVisible()) await page.getByRole('button', { name: 'Dismiss notice', exact: true }).click();
    await page.screenshot({ path: resolve(evidence, 'workspace-desktop.png'), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: resolve(evidence, 'workspace-mobile.png'), fullPage: true });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'Mobile layout has no horizontal page overflow');
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('button', { name: /^Scenarios/ }).click();
    await page.getByRole('button', { name: 'Ask AI to propose', exact: true }).click();
    await page.getByText('Proposal generation is unavailable', { exact: true }).waitFor();
    await page.getByRole('button', { name: 'Close AI proposals' }).click();
    // Only generation is simulated here. The review/save uses the real API/database.
    // Provider HTTP/schema/error behaviour is separately covered by Go tests.
    await page.route('**/api/ai/config', route => route.fulfill({ json: {
      provider: 'openai', models: ['test-model'], managed_available: false, byok_available: true,
    } }));
    const proposal = { ...templates[0], name: 'Proposed independent revenue check', approved: false };
    await page.route('**/proposals', async route => {
      assert.equal(route.request().headers()['x-qa-provider-key'], 'synthetic-provider-key');
      assert.equal(route.request().postDataJSON().consent, true);
      await route.fulfill({ json: { provider: 'openai', model: 'test-model', context_sha256: 'synthetic-context', scenarios: [proposal], questions: ['Are multi-currency orders supported?'], assumptions: ['All fixture orders use one currency.'] } });
    });
    await page.getByRole('button', { name: 'Ask AI to propose', exact: true }).click();
    await page.getByRole('textbox', { name: /^Testing request/ }).fill('Check the independent net revenue calculation.');
    await page.getByRole('textbox', { name: /^Application context/ }).fill('Paid 150000 minus refund 10000 equals 140000; cancelled 90000 is excluded.');
    await page.getByLabel('OpenAI API key', { exact: true }).fill('synthetic-provider-key');
    await page.getByRole('checkbox', { name: /I agree to send/ }).check();
    await page.getByRole('button', { name: 'Generate proposals', exact: true }).click();
    await page.getByText('Are multi-currency orders supported?', { exact: true }).waitFor();
    assert.equal(await page.getByLabel('OpenAI API key', { exact: true }).inputValue(), '');
    await page.getByRole('button', { name: 'Review in editor', exact: true }).click();
    const approvedBox = page.getByRole('checkbox', { name: 'Approve this scenario for runs', exact: true });
    assert.equal(await approvedBox.isChecked(), false, 'AI draft must not approve itself');
    await approvedBox.check();
    await page.getByRole('button', { name: 'Create scenario', exact: true }).click();
    await page.getByText('Confirm you reviewed the expected outcomes and assertions before approving.', { exact: true }).waitFor();
    await page.getByRole('checkbox', { name: /I reviewed the expected outcome/ }).check();
    await page.getByRole('button', { name: 'Create scenario', exact: true }).click();
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    const saved = await api(`/api/projects/${healthy.project.id}/scenarios`);
    assert.equal(saved.find(s => s.name === proposal.name)?.approved, true);
    observations.push({ name: 'AI draft review and approval UI (simulated generation, real persistence)', status: 'passed' });
    assert.deepEqual(errors, [], 'AI proposal flow must not throw browser errors');
    assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0, 'Operator token must not enter browser storage');
    await page.reload();
    await page.getByLabel('API token', { exact: true }).waitFor();
    observations.push({ name: 'Web workspace launches real checks and clears token on reload', status: 'passed' });
  } finally { await browser.close(); }
  console.log(JSON.stringify(observations, null, 2));
} finally {
  for (const child of children.reverse()) await stop(child);
  for (const file of files) await file.close();
  if (containerCreated) command('docker', ['rm', '-f', '-v', container]);
  manifest.cleanup = true;
  manifest.finished_at = new Date().toISOString();
  await writeFile(resolve(evidence, 'manifest.json'), JSON.stringify(manifest, null, 2));
  await writeFile(resolve(evidence, 'observations.json'), JSON.stringify(observations, null, 2));
  console.log(`Evidence: ${evidence}`);
}
