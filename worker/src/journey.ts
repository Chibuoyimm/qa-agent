import type { Browser, Page } from 'playwright';
import { mkdir, rename } from 'node:fs/promises';
import { join, relative, sep } from 'node:path';
import { openGuardedPage } from './browser-context.js';
import {
  safePart,
  validateBaseUrl,
  bindingToken,
  safeStepPath,
  decimalLiteral,
  type Run,
  type Scenario,
  type Step,
  type ScenarioResult,
  type ExecutionStepResult,
  type Artifact
} from './contract.js';
import { stepLocator } from './locator.js';
import { RunFixture, type FixtureProfile } from './fixtures.js';

const TIMEOUT = 8000;
class Failed extends Error {
  constructor(
    message: string,
    readonly observed = ''
  ) {
    super(message);
  }
}
const identifier = /^[A-Za-z0-9_-]{1,128}$/;
function decimal(value: unknown): string {
  const text = String(value);
  if (
    (typeof value !== 'string' && typeof value !== 'number') ||
    !decimalLiteral.test(text) ||
    (typeof value === 'number' &&
      (!Number.isFinite(value) || Math.abs(value) > Number.MAX_SAFE_INTEGER))
  )
    throw new Error('Expected a bounded decimal value');
  const negative = text.startsWith('-');
  const [whole, fraction = ''] = text.replace(/^[+-]/, '').split('.');
  const normalized =
    (whole.replace(/^0+(?=\d)/, '') || '0') +
    (fraction.replace(/0+$/, '') ? '.' + fraction.replace(/0+$/, '') : '');
  return negative && normalized !== '0' ? '-' + normalized : normalized;
}
export { decimal };
function pointer(data: unknown, path: string): unknown {
  let value = data;
  for (const part of path
    .slice(1)
    .split('/')
    .map((p) => p.replace(/~1/g, '/').replace(/~0/g, '~'))) {
    if (!value || typeof value !== 'object' || !Object.hasOwn(value, part))
      throw new Failed('The approved JSON field is missing');
    value = (value as Record<string, unknown>)[part];
  }
  if (value !== null && !['string', 'number', 'boolean'].includes(typeof value))
    throw new Failed('The approved JSON field is not a scalar');
  return value;
}
export async function executeJourney(
  browser: Browser,
  run: Run,
  scenario: Scenario,
  origins: Set<string>,
  root: string,
  signal: AbortSignal,
  profiles: FixtureProfile[],
  progress?: (result: ScenarioResult) => Promise<void>
): Promise<ScenarioResult> {
  const started = Date.now(),
    base = validateBaseUrl(run.base_url, origins),
    records: ExecutionStepResult[] = [],
    artifacts: Artifact[] = [];
  const bindings = new Map<string, string>([['run_id', run.id]]),
    secrets: Record<string, string | undefined> = { ...process.env };
  const actors = new Map<string, Awaited<ReturnType<typeof openGuardedPage>>>();
  const precedingURLs = new Map<string, string>();
  let actor = 'default',
    fixture: RunFixture | undefined;
  const prepared =
    scenario.steps[0]?.action === 'prepare_fixture'
      ? profiles.find((p) => p.id === scenario.steps[0].value && p.origins.includes(base.origin))
      : undefined;
  const missing = scenario.steps.some(
    (s) => s.secret_env && !secrets[s.secret_env] && !prepared?.secret_refs.includes(s.secret_env)
  );
  if (missing || (scenario.steps[0]?.action === 'prepare_fixture' && !prepared))
    return {
      scenario_id: scenario.id,
      status: 'blocked',
      message: missing
        ? 'A referenced QA_TEST_ secret is unavailable'
        : 'The approved test-data profile is unavailable for this origin',
      duration_ms: 0,
      artifacts,
      step_results: []
    };
  let status: ScenarioResult['status'] = 'passed',
    message = 'All approved journey steps passed',
    stopped = false;
  const timeout = AbortSignal.timeout(10 * 60 * 1000),
    combined = AbortSignal.any([signal, timeout]);
  const stop = () => {
    for (const entry of actors.values()) void entry.page.close().catch(() => {});
  };
  combined.addEventListener('abort', stop, { once: true });
  const resolve = (text: string, path = false) =>
    text.replace(bindingToken, (_, name: string) => {
      const value = bindings.get(name);
      if (value === undefined) throw new Error('A required journey identifier is unavailable');
      return path ? encodeURIComponent(value) : value;
    });
  const url = (path: string) => {
    const expanded = resolve(path, true);
    if (!safeStepPath(expanded)) throw new Error('Resolved path is outside the approved boundary');
    const target = new URL(expanded, base);
    if (target.origin !== base.origin || target.username || target.password)
      throw new Error('Resolved origin is not approved');
    return target;
  };
  const pageFor = async () => {
    let entry = actors.get(actor);
    if (!entry) {
      const dir = join(root, safePart(run.id), safePart(scenario.id), safePart(actor));
      await mkdir(dir, { recursive: true });
      entry = await openGuardedPage(browser, origins, {
        viewport: { width: 1280, height: 720 },
        recordVideo: { dir, size: { width: 1280, height: 720 } }
      });
      actors.set(actor, entry);
    }
    return entry.page;
  };
  const snapshot = (): ScenarioResult => ({
    scenario_id: scenario.id,
    status: stopped ? status : 'running',
    message: stopped ? message : 'Executing the approved journey',
    duration_ms: Date.now() - started,
    artifacts: [...artifacts],
    step_results: [...records]
  });
  const checkpoint = async () => {
    for (const entry of actors.values())
      if (entry.violation()) throw new Error('The target attempted an unsupported browser request');
    if (progress) await progress(snapshot());
  };
  async function waitPath(page: Page, step: Step, capture: boolean) {
    const target = resolve(step.path!, true),
      deadline = Date.now() + TIMEOUT;
    while (Date.now() < deadline) {
      combined.throwIfAborted();
      const current = new URL(page.url());
      if (current.origin === base.origin && !current.search && !current.hash) {
        if (!capture && current.pathname === target) return;
        const id = current.pathname.slice(target.length);
        if (
          capture &&
          current.pathname.startsWith(target) &&
          current.href !== precedingURLs.get(actor) &&
          identifier.test(id)
        ) {
          bindings.set(step.save_as!, id);
          return;
        }
      }
      await page.waitForTimeout(100);
    }
    throw new Failed(
      capture
        ? 'The created record URL did not match the approved prefix'
        : 'The page did not reach the approved path'
    );
  }
  async function readJSON(page: Page, step: Step): Promise<unknown> {
    const target = url(step.path!);
    if (new URL(page.url()).origin !== base.origin)
      throw new Error('Navigate the current actor to the app before reading saved values');
    // Use the browser's own cookie and security rules, including Secure cookies on loopback.
    const response = await page.evaluate(
      async ({ href, timeout }) => {
        const response = await fetch(href, {
          method: 'GET',
          credentials: 'same-origin',
          redirect: 'error',
          cache: 'no-store',
          signal: AbortSignal.timeout(timeout)
        });
        if (!response.ok || !response.headers.get('content-type')?.includes('json')) {
          await response.body?.cancel();
          return { ok: false, raw: '', status: response.status };
        }
        if (Number(response.headers.get('content-length') ?? 0) > 65536) {
          await response.body?.cancel();
          throw new Error('JSON response exceeds the read budget');
        }
        const reader = response.body!.getReader();
        const decoder = new TextDecoder();
        let raw = '',
          bytes = 0;
        try {
          while (true) {
            const chunk = await reader.read();
            if (chunk.done) break;
            bytes += chunk.value.byteLength;
            if (bytes > 65536) throw new Error('JSON response exceeds the read budget');
            raw += decoder.decode(chunk.value, { stream: true });
          }
          return { ok: true, raw: raw + decoder.decode(), status: response.status };
        } finally {
          await reader.cancel();
        }
      },
      { href: target.href, timeout: TIMEOUT }
    );
    if (!response.ok)
      throw new Failed(`The approved JSON read was unsuccessful (HTTP ${response.status})`);
    const document = JSON.parse(
      response.raw,
      (_key, value: unknown, context?: { source?: string }) => {
        // Preserve the original decimal token before JSON.parse rounds it to a double.
        if (typeof value === 'number') {
          if (!context?.source)
            throw new Error('Exact JSON numbers require the supported Node runtime');
          return context.source;
        }
        return value;
      }
    );
    return pointer(document, step.json_pointer!);
  }
  for (const [index, step] of scenario.steps.entries()) {
    if (step.action === 'cleanup_fixture') break;
    if (stopped) {
      records.push({
        index,
        action: step.action,
        status: 'not_run',
        duration_ms: 0,
        message: 'An earlier required step did not pass'
      });
      continue;
    }
    const begin = Date.now();
    try {
      combined.throwIfAborted();
      if (step.action === 'phase') {
        // Record the stage marker before publishing progress so it appears active.
      } else if (step.action === 'use_actor') {
        actor = step.value!;
      } else if (step.action === 'prepare_fixture') {
        fixture = new RunFixture(prepared!, run.id, base.origin);
        Object.assign(secrets, await fixture.prepare(combined));
      } else {
        const page = await pageFor(),
          target = step.test_id || step.locator ? stepLocator(page, step) : undefined;
        switch (step.action) {
          case 'navigate':
            precedingURLs.set(actor, page.url());
            await page.goto(url(step.path!).href, {
              waitUntil: 'networkidle',
              timeout: TIMEOUT
            });
            break;
          case 'capture_id':
            await waitPath(page, step, true);
            break;
          case 'assert_path':
            await waitPath(page, step, false);
            break;
          case 'fill':
            await target!.fill(step.secret_env ? secrets[step.secret_env]! : resolve(step.value!), {
              timeout: TIMEOUT
            });
            break;
          case 'click':
            precedingURLs.set(actor, page.url());
            await target!.click({ timeout: TIMEOUT });
            break;
          case 'select_option':
            await target!.selectOption(step.value!, { timeout: TIMEOUT });
            break;
          case 'assert_visible':
            try {
              await target!.waitFor({ state: 'visible', timeout: TIMEOUT });
            } catch {
              throw new Failed('Expected control was not visible');
            }
            break;
          case 'assert_absent':
            try {
              await target!.waitFor({ state: 'hidden', timeout: TIMEOUT });
            } catch {
              throw new Failed('A forbidden control remained visible');
            }
            break;
          case 'assert_text': {
            let observed = '';
            const deadline = Date.now() + TIMEOUT;
            while (Date.now() < deadline) {
              combined.throwIfAborted();
              try {
                observed = await target!.innerText({ timeout: 500 });
                if (observed === step.value) break;
              } catch {}
              await page.waitForTimeout(100);
            }
            if (observed !== step.value)
              throw new Failed('Text did not match the approved expected value');
            break;
          }
          case 'capture_json': {
            const value = await readJSON(page, step);
            if (typeof value !== 'string' || !identifier.test(value))
              throw new Failed('The captured identifier is invalid');
            bindings.set(step.save_as!, value);
            break;
          }
          case 'assert_json':
          case 'assert_json_number': {
            const value = await readJSON(page, step),
              expected = step.value_ref ? bindings.get(step.value_ref) : step.value;
            let equal = false;
            try {
              equal =
                step.action === 'assert_json_number'
                  ? decimal(value) === decimal(expected)
                  : String(value) === expected;
            } catch {
              throw new Failed('The saved JSON field is not a valid decimal amount');
            }
            if (!equal)
              throw new Failed('The saved JSON value did not match the approved expectation');
            break;
          }
        }
      }
      for (const entry of actors.values())
        if (entry.violation()) throw new Error('Target boundary violation');
      records.push({
        index,
        action: step.action,
        status: 'passed',
        duration_ms: Date.now() - begin
      });
      if (step.action === 'phase' || step.action === 'prepare_fixture') await checkpoint();
    } catch (error) {
      stopped = true;
      status = !combined.aborted && error instanceof Failed ? 'failed' : 'error';
      message = combined.aborted
        ? 'Journey interrupted or timed out'
        : error instanceof Failed
          ? error.message
          : 'Journey action could not complete';
      if (records.at(-1)?.index === index) records.pop();
      records.push({
        index,
        action: step.action,
        status: status as 'failed' | 'error',
        duration_ms: Date.now() - begin,
        message
      });
    }
  }
  // Finish every actor's media before removing owned application data.
  combined.removeEventListener('abort', stop);
  for (const [name, entry] of actors) {
    const dir = join(root, safePart(run.id), safePart(scenario.id), safePart(name));
    const video = entry.page.video();
    try {
      if (!entry.page.isClosed()) {
        const path = join(dir, 'final.png');
        await entry.page.screenshot({ path, timeout: 3000 });
        artifacts.push({
          kind: 'screenshot',
          actor: name,
          path: relative(root, path).split(sep).join('/')
        });
      }
    } catch {}
    await entry.context.close().catch(() => {});
    if (video)
      try {
        const path = join(dir, 'session.webm');
        await rename(await video.path(), path);
        artifacts.push({
          kind: 'video',
          actor: name,
          path: relative(root, path).split(sep).join('/')
        });
      } catch {}
  }
  const cleanup = scenario.steps.at(-1);
  if (cleanup?.action === 'cleanup_fixture') {
    const begin = Date.now();
    try {
      await fixture?.cleanup();
      records.push({
        index: scenario.steps.length - 1,
        action: 'cleanup_fixture',
        status: 'passed',
        duration_ms: Date.now() - begin
      });
    } catch {
      status = 'error';
      message = 'Test-data cleanup failed; operator attention is required';
      records.push({
        index: scenario.steps.length - 1,
        action: 'cleanup_fixture',
        status: 'error',
        duration_ms: Date.now() - begin,
        message
      });
    }
  }
  const result: ScenarioResult = {
    scenario_id: scenario.id,
    status,
    message,
    duration_ms: Date.now() - started,
    artifacts,
    step_results: records
  };
  if (progress) await progress(result).catch(() => {});
  return result;
}
