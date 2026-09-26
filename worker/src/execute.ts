import { mkdir, rename } from 'node:fs/promises';
import { join, relative, sep } from 'node:path';
import type { Browser, BrowserContext, Page } from 'playwright';
import { safePart, validateBaseUrl, type Artifact, type Run, type Scenario, type ScenarioResult } from './contract.js';

const STEP_TIMEOUT_MS = 8000;
const SCENARIO_TIMEOUT_MS = 60000;

class AssertionFailure extends Error {}
class MissingSecret extends Error {}
class ForbiddenRequest extends Error {}

function artifactPath(root: string, file: string): string {
  const path = relative(root, file).split(sep).join('/');
  if (!path || path.startsWith('../') || path.startsWith('/') || path.includes('..')) throw new Error('Invalid artifact path');
  return path;
}

function isAllowedRequest(url: string, origins: Set<string>): boolean {
  let parsed: URL;
  try { parsed = new URL(url); } catch { return false; }
  if (parsed.protocol === 'data:' || parsed.protocol === 'blob:') return true;
  return ['http:', 'https:'].includes(parsed.protocol) && origins.has(parsed.origin);
}

function isAllowedMainFrame(url: string, origins: Set<string>): boolean {
  try {
    const parsed = new URL(url);
    return ['http:', 'https:'].includes(parsed.protocol) && origins.has(parsed.origin);
  } catch { return false; }
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

async function executeSteps(page: Page, scenario: Scenario, base: URL, signal: AbortSignal): Promise<void> {
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
  let forbidden = false;
  let unsupportedSocket = false;
  let popupAttempt = false;
  let unsupportedFrame = false;
  const timeout = AbortSignal.timeout(SCENARIO_TIMEOUT_MS);
  const combined = AbortSignal.any([signal, timeout]);
  const stop = () => { void page?.close().catch(() => {}); };
  combined.addEventListener('abort', stop, { once: true });
  try {
    context = await browser.newContext({
      serviceWorkers: 'block',
      recordVideo: { dir: directory, size: { width: 1280, height: 720 } },
      viewport: { width: 1280, height: 720 },
    });
    await context.routeWebSocket(/.*/, async socket => {
      // This pilot has no WebSocket scenarios. Reject handshakes before they reach a target.
      unsupportedSocket = true;
      await socket.close();
    });
    page = await context.newPage();
    const mainPage = page;
    context.on('page', opened => {
      if (opened !== mainPage) {
        popupAttempt = true;
        void opened.close().catch(() => {});
      }
    });
    await context.route('**/*', async route => {
      // CDP covers the main page and native redirects. Keep a context-level guard
      // for direct off-origin requests and targets outside that CDP session.
      if (!isAllowedRequest(route.request().url(), allowedOrigins)) {
        forbidden = true;
        await route.abort('blockedbyclient').catch(() => {});
        return;
      }
      try {
        const frame = route.request().frame();
        if (frame.page() !== mainPage) {
          popupAttempt = true;
          await route.abort('blockedbyclient');
          return;
        }
        if (frame !== mainPage.mainFrame()) {
          // A page CDP session need not cover a cross-process iframe's redirects.
          unsupportedFrame = true;
          await route.abort('blockedbyclient');
          return;
        }
      } catch {
        popupAttempt = true;
        await route.abort('blockedbyclient').catch(() => {});
        return;
      }
      await route.continue().catch(() => {});
    });
    const cdp = await context.newCDPSession(page);
    cdp.on('Fetch.requestPaused', ({ requestId, request }: { requestId: string; request: { url: string } }) => {
      if (!isAllowedRequest(request.url, allowedOrigins)) {
        forbidden = true;
        void cdp.send('Fetch.failRequest', { requestId, errorReason: 'BlockedByClient' }).catch(() => {});
      } else {
        void cdp.send('Fetch.continueRequest', { requestId }).catch(() => {});
      }
    });
    await cdp.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }] });
    page.on('framenavigated', frame => {
      if (frame === page?.mainFrame() && frame.url() !== 'about:blank' && !isAllowedMainFrame(frame.url(), allowedOrigins)) {
        forbidden = true;
        void page?.close().catch(() => {});
      }
    });
    await executeSteps(page, scenario, base, combined);
    if (forbidden) throw new ForbiddenRequest('Target attempted a request outside allowed origins');
    if (unsupportedSocket) throw new Error('Unsupported WebSocket');
    if (popupAttempt) throw new Error('Popup unsupported');
    if (unsupportedFrame) throw new Error('Iframe unsupported');
  } catch (error) {
    if (forbidden || error instanceof ForbiddenRequest) {
      status = 'error'; message = 'Target requested an origin outside the worker allowlist';
    } else if (error instanceof MissingSecret) {
      status = 'blocked'; message = 'A referenced QA_TEST_ secret is unavailable';
    } else if (unsupportedSocket) {
      status = 'error'; message = 'Target attempted a WebSocket connection, unsupported in this pilot';
    } else if (popupAttempt) {
      status = 'error'; message = 'Target attempted a popup, unsupported in this pilot';
    } else if (unsupportedFrame) {
      status = 'error'; message = 'Target attempted an iframe request, unsupported in this pilot';
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
  return { scenario_id: scenario.id, status, message, duration_ms: Date.now() - started, artifacts };
}

export function errorResult(scenario: Scenario, message: string): ScenarioResult {
  return { scenario_id: scenario.id, status: 'error', message, duration_ms: 0, artifacts: [] };
}
