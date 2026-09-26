import type { Browser, BrowserContextOptions, Page } from 'playwright';

function allowedRequest(value: string, origins: Set<string>): boolean {
  try {
    const url = new URL(value);
    if (url.protocol === 'data:' || url.protocol === 'blob:') return true;
    return ['http:', 'https:'].includes(url.protocol) && origins.has(url.origin) && !url.username && !url.password;
  } catch { return false; }
}

// Test execution and discovery must enforce the same boundary, including native redirects.
export async function openGuardedPage(browser: Browser, origins: Set<string>, options: BrowserContextOptions = {}) {
  const context = await browser.newContext({ ...options, serviceWorkers: 'block' });
  let page: Page;
  let forbidden = false, socketAttempt = false, popupAttempt = false, frameAttempt = false, writeAttempt = false;
  let readsOnly = false;
  const permits = (url: string, method: string) => {
    if (!allowedRequest(url, origins)) { forbidden = true; return false; }
    if (readsOnly && !['GET', 'HEAD', 'OPTIONS'].includes(method)) { writeAttempt = true; return false; }
    return true;
  };
  try {
    await context.routeWebSocket(/.*/, async socket => { socketAttempt = true; await socket.close(); });
    page = await context.newPage();
    context.on('page', opened => {
      if (opened !== page) { popupAttempt = true; void opened.close().catch(() => {}); }
    });
    await context.route('**/*', async route => {
      if (!permits(route.request().url(), route.request().method())) {
        await route.abort('blockedbyclient').catch(() => {}); return;
      }
      try {
        const frame = route.request().frame();
        if (frame.page() !== page) { popupAttempt = true; await route.abort('blockedbyclient'); return; }
        if (frame !== page.mainFrame()) { frameAttempt = true; await route.abort('blockedbyclient'); return; }
      } catch {
        popupAttempt = true; await route.abort('blockedbyclient').catch(() => {}); return;
      }
      await route.continue().catch(() => {});
    });
    const cdp = await context.newCDPSession(page);
    cdp.on('Fetch.requestPaused', ({ requestId, request }: { requestId: string; request: { url: string; method: string } }) => {
      if (permits(request.url, request.method)) {
        void cdp.send('Fetch.continueRequest', { requestId }).catch(() => {});
      } else {
        void cdp.send('Fetch.failRequest', { requestId, errorReason: 'BlockedByClient' }).catch(() => {});
      }
    });
    await cdp.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }] });
    page.on('framenavigated', frame => {
      if (frame !== page.mainFrame() || frame.url() === 'about:blank') return;
      try {
        const url = new URL(frame.url());
        if (['http:', 'https:'].includes(url.protocol) && origins.has(url.origin) && !url.username && !url.password) return;
      } catch { /* An unparsable document URL cannot be inside the boundary. */ }
      forbidden = true; void page.close().catch(() => {});
    });
    return {
      context, page,
      restrictToReads() { readsOnly = true; },
      violation(): string {
        if (forbidden) return 'Target requested an origin outside the worker allowlist';
        if (socketAttempt) return 'Target attempted a WebSocket connection, unsupported in this pilot';
        if (popupAttempt) return 'Target attempted a popup, unsupported in this pilot';
        if (frameAttempt) return 'Target attempted an iframe request, unsupported in this pilot';
        if (writeAttempt) return 'Discovery blocked a non-read request after approved setup';
        return '';
      },
    };
  } catch (error) {
    await context.close().catch(() => {});
    throw error;
  }
}
