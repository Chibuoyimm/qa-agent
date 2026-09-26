import { mkdir, rename } from 'node:fs/promises';
import { join, relative, sep } from 'node:path';
import type { Browser, BrowserContext, Page } from 'playwright';
import { openGuardedPage } from './browser-context.js';
import { safePart, validateBaseUrl, type Artifact, type Run, type Scenario, type ScenarioResult } from './contract.js';

const STEP_TIMEOUT_MS = 8000;
const SCENARIO_TIMEOUT_MS = 60000;

class AssertionFailure extends Error {}
class MissingSecret extends Error {}

function artifactPath(root: string, file: string): string {
  const path = relative(root, file).split(sep).join('/');
  if (!path || path.startsWith('../') || path.startsWith('/') || path.includes('..')) throw new Error('Invalid artifact path');
  return path;
}

async function assertText(page: Page, testId: string, expected: string): Promise<void> {
  const locator = page.getByTestId(testId);
  try { await locator.waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS }); }
  catch { throw new AssertionFailure('Expected element was not visible'); }
  const deadline = Date.now() + STEP_TIMEOUT_MS;
  while (Date.now() < deadline) {
    try {
      if (await locator.innerText({ timeout: 1000 }) === expected) return;
    } catch { /* A transient rerender may replace the element. */ }
    await page.waitForTimeout(100);
  }
  throw new AssertionFailure('Text did not match the approved expected value');
}

export async function executeSteps(page: Page, scenario: Scenario, base: URL, signal: AbortSignal): Promise<void> {
  for (const step of scenario.steps) {
    if (signal.aborted) throw new Error('Execution interrupted');
    switch (step.action) {
      case 'navigate':
        await page.goto(new URL(step.path!, base).href, { waitUntil: 'domcontentloaded', timeout: STEP_TIMEOUT_MS });
        break;
      case 'fill': {
        const value = step.secret_env ? process.env[step.secret_env] : step.value;
        if (step.secret_env && !value) throw new MissingSecret('Referenced test secret is unavailable');
        await page.getByTestId(step.test_id!).fill(value ?? '', { timeout: STEP_TIMEOUT_MS });
        break;
      }
      case 'click':
        await page.getByTestId(step.test_id!).click({ timeout: STEP_TIMEOUT_MS });
        break;
      case 'assert_visible':
        try { await page.getByTestId(step.test_id!).waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS }); }
        catch { throw new AssertionFailure('Expected element was not visible'); }
        break;
      case 'assert_text':
        await assertText(page, step.test_id!, step.value!);
        break;
    }
  }
}

export async function executeScenario(browser: Browser, run: Run, scenario: Scenario, allowedOrigins: Set<string>, artifactRoot: string, signal: AbortSignal): Promise<ScenarioResult> {
  const started = Date.now();
  const artifacts: Artifact[] = [];
  const base = validateBaseUrl(run.base_url, allowedOrigins);
  if (scenario.steps.some(step => step.secret_env && !process.env[step.secret_env])) {
    return { scenario_id: scenario.id, status: 'blocked', message: 'A referenced QA_TEST_ secret is unavailable', duration_ms: Date.now() - started, artifacts };
  }
  const directory = join(artifactRoot, safePart(run.id), safePart(scenario.id));
  await mkdir(directory, { recursive: true });
  let context: BrowserContext | undefined;
  let page: Page | undefined;
  let status: ScenarioResult['status'] = 'passed';
  let message = 'Approved checks passed';
  let guarded: Awaited<ReturnType<typeof openGuardedPage>> | undefined;
  const timeout = AbortSignal.timeout(SCENARIO_TIMEOUT_MS);
  const combined = AbortSignal.any([signal, timeout]);
  const stop = () => { void page?.close().catch(() => {}); };
  combined.addEventListener('abort', stop, { once: true });
  try {
    guarded = await openGuardedPage(browser, allowedOrigins, {
      recordVideo: { dir: directory, size: { width: 1280, height: 720 } },
      viewport: { width: 1280, height: 720 },
    });
    context = guarded.context;
    page = guarded.page;
    await executeSteps(page, scenario, base, combined);
    if (guarded.violation()) throw new Error(guarded.violation());
  } catch (error) {
    if (guarded?.violation()) {
      status = 'error'; message = guarded.violation();
    } else if (error instanceof MissingSecret) {
      status = 'blocked'; message = 'A referenced QA_TEST_ secret is unavailable';
    } else if (error instanceof AssertionFailure) {
      status = 'failed'; message = error.message;
    } else {
      status = 'error';
      message = timeout.aborted ? 'Scenario exceeded its time limit' : signal.aborted ? 'Execution interrupted' : 'Browser action or target request failed';
    }
  } finally {
    combined.removeEventListener('abort', stop);
    if (page && !page.isClosed()) {
      const screenshot = join(directory, 'final.png');
      try {
        await page.screenshot({ path: screenshot, timeout: 3000 });
        artifacts.push({ kind: 'screenshot', path: artifactPath(artifactRoot, screenshot) });
      } catch { /* A closed/crashed page has no screenshot. */ }
    }
    const video = page?.video();
    if (context) await context.close().catch(() => {});
    if (video) {
      try {
        const source = await video.path();
        const destination = join(directory, 'session.webm');
        await rename(source, destination);
        artifacts.push({ kind: 'video', path: artifactPath(artifactRoot, destination) });
      } catch { /* Video may be missing after a browser crash. */ }
    }
  }
  if (guarded?.violation()) { status = 'error'; message = guarded.violation(); }
  return { scenario_id: scenario.id, status, message, duration_ms: Date.now() - started, artifacts };
}

export function errorResult(scenario: Scenario, message: string): ScenarioResult {
  return { scenario_id: scenario.id, status: 'error', message, duration_ms: 0, artifacts: [] };
}
