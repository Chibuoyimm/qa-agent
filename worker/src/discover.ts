import type { Browser } from 'playwright';
import { openGuardedPage } from './browser-context.js';
import { executeSteps } from './execute.js';
import { parseScenario, validateBaseUrl, type Scenario } from './contract.js';

export type DiscoveryInput = { base_url: string; start_path: string; max_pages: number; setup_scenario?: Scenario };
export type PageObservation = {
  path: string;
  title: string;
  headings: string[];
  elements: { test_id: string; tag: string; role: string; label: string; text: string; input_type: string }[];
  links: { path: string; text: string }[];
  truncated: boolean;
};
export type DiscoveryResult = { pages: PageObservation[]; limited: boolean; warnings: string[] };

export function safeDiscoveryPath(path: string): boolean {
  return path.startsWith('/') && !path.startsWith('//') && path.length <= 2048 && !/[?#\\\u0000-\u0020]/.test(path) && !path.includes('..');
}

// Only approved setup steps may interact. Exploration follows ordinary same-origin
// links and blocks writes, but GET endpoints can still have application side effects.
export async function discover(browser: Browser, input: DiscoveryInput, origins: Set<string>, signal: AbortSignal): Promise<DiscoveryResult> {
  const base = validateBaseUrl(input.base_url, origins);
  if (!safeDiscoveryPath(input.start_path) || !Number.isInteger(input.max_pages) || input.max_pages < 1 || input.max_pages > 5) throw new Error('Invalid discovery scope');
  const setup = input.setup_scenario ? parseScenario(input.setup_scenario) : undefined;
  const secrets = [...new Set(setup?.steps.flatMap(step => step.secret_env ? [process.env[step.secret_env]] : []) ?? [])];
  if (secrets.some(secret => !secret)) throw new Error('A referenced QA_TEST_ secret is unavailable');
  signal.throwIfAborted();
  const guarded = await openGuardedPage(browser, origins, { viewport: { width: 1280, height: 720 } });
  const { page, context } = guarded;
  const deadline = AbortSignal.timeout(60000);
  const combined = AbortSignal.any([signal, deadline]);
  const stop = () => { void context.close().catch(() => {}); };
  combined.addEventListener('abort', stop, { once: true });
  try {
    combined.throwIfAborted();
    if (setup) await executeSteps(page, setup, base, combined);
    if (guarded.violation()) throw new Error(guarded.violation());
    guarded.restrictToReads();
    const queue = [input.start_path];
    const queued = new Set(queue), visited = new Set<string>();
    const pages: PageObservation[] = [];
    const warnings = new Set<string>();
    let limited = false;
    while (queue.length && pages.length < input.max_pages) {
      combined.throwIfAborted();
      const path = queue.shift()!;
      if (visited.has(path)) continue;
      visited.add(path);
      const response = await page.goto(new URL(path, base).href, { waitUntil: 'networkidle', timeout: 10000 });
      if (guarded.violation()) throw new Error(guarded.violation());
      if (!response?.ok()) throw new Error('Discovery page returned an unsuccessful response');
      const actual = new URL(page.url());
      if (actual.origin !== base.origin || !safeDiscoveryPath(actual.pathname) || actual.search || actual.hash) throw new Error('Discovery navigation left its page scope');
      if (actual.pathname !== path && visited.has(actual.pathname)) continue;
      visited.add(actual.pathname);
      const observation = await page.evaluate(() => {
        const candidates = [...document.querySelectorAll('[data-testid],button,input,select,textarea,a,[role]')].filter(element => element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden');
        const headings = [...document.querySelectorAll('h1,h2,h3')].filter(element => element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden');
        const anchors = [...document.querySelectorAll<HTMLAnchorElement>('a[href]')].filter(element => element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden');
        return {
          path: location.pathname,
          title: document.title.replace(/\s+/g, ' ').trim().slice(0, 200),
          headings: headings.slice(0, 20).map(element => ((element as HTMLElement).innerText ?? '').replace(/\s+/g, ' ').trim().slice(0, 300)),
          elements: candidates.slice(0, 80).map(element => ({
            test_id: (element.getAttribute('data-testid') ?? '').slice(0, 200),
            tag: element.tagName.toLowerCase(),
            role: (element.getAttribute('role') ?? '').slice(0, 100),
            label: (element.getAttribute('aria-label') ?? (element as HTMLInputElement).labels?.[0]?.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 200),
            // Never collect input values, hidden fields, cookies or browser storage.
            text: ['INPUT', 'TEXTAREA', 'SELECT'].includes(element.tagName) ? '' : ((element as HTMLElement).innerText ?? '').replace(/\s+/g, ' ').trim().slice(0, 300),
            input_type: element.tagName === 'INPUT' ? (element.getAttribute('type') ?? 'text').slice(0, 50) : '',
          })),
          links: anchors.slice(0, 80).map(a => ({ path: a.href, text: a.innerText.replace(/\s+/g, ' ').trim().slice(0, 300) })),
          truncated: candidates.length > 80 || headings.length > 20 || anchors.length > 80 || document.title.length > 200 ||
            candidates.some(element => (element as HTMLElement).innerText?.length > 300 || (element.getAttribute('aria-label')?.length ?? 0) > 200 || (element.getAttribute('data-testid')?.length ?? 0) > 200) ||
            headings.some(element => (element as HTMLElement).innerText?.length > 300),
        };
      });
      const links: PageObservation['links'] = [];
      for (const link of observation.links) {
        const url = new URL(link.path, base);
        if (url.origin !== base.origin || url.search || url.hash || !safeDiscoveryPath(url.pathname) || /(?:logout|signout|sign-out|delete|remove|destroy|unsubscribe)/i.test(url.pathname)) {
          warnings.add('External, parameterized, fragment, or potentially state-changing links were not followed.'); continue;
        }
        links.push({ path: url.pathname, text: link.text });
        if (!queued.has(url.pathname)) { queued.add(url.pathname); queue.push(url.pathname); }
      }
      observation.links = links;
      const redact = (value: string) => secrets.reduce<string>((text, secret) => secret ? text.split(secret).join('[redacted]') : text, value);
      const sanitized: PageObservation = {
        ...observation,
        path: redact(observation.path),
        title: redact(observation.title).slice(0, 200),
        headings: observation.headings.map(text => redact(text).slice(0, 300)),
        elements: observation.elements.map(element => ({
          test_id: redact(element.test_id).slice(0, 200), tag: redact(element.tag).slice(0, 100), role: redact(element.role).slice(0, 100),
          text: redact(element.text).slice(0, 300), label: redact(element.label).slice(0, 200), input_type: redact(element.input_type).slice(0, 50),
        })),
        links: observation.links.map(link => ({ path: redact(link.path), text: redact(link.text).slice(0, 300) })),
      };
      if (Buffer.byteLength(JSON.stringify([...pages, sanitized]), 'utf8') > 40000) {
        limited = true; warnings.add('The discovery content budget was reached; remaining observations were omitted.'); break;
      }
      pages.push(sanitized);
      if (sanitized.truncated) { limited = true; warnings.add('A page exceeded the element or text limits; observations are partial.'); }
    }
    if (queue.some(path => !visited.has(path))) { limited = true; warnings.add('The page limit was reached; additional linked pages were not visited.'); }
    if (!pages.length) throw new Error('Discovery did not produce a page observation');
    if (guarded.violation()) throw new Error(guarded.violation());
    return { pages, limited, warnings: [...warnings] };
  } finally {
    combined.removeEventListener('abort', stop);
    await context.close().catch(() => {});
    if (guarded.violation()) throw new Error(guarded.violation());
  }
}

export type DiscoveryClaim = {
  discovery: DiscoveryInput & { id: string };
  lease_token: string;
};

export function parseDiscoveryClaim(value: unknown): DiscoveryClaim {
  if (!value || typeof value !== 'object') throw new Error('Invalid discovery claim');
  const claim = value as Record<string, unknown>;
  if (!claim.discovery || typeof claim.discovery !== 'object' || typeof claim.lease_token !== 'string' || !claim.lease_token) throw new Error('Invalid discovery claim');
  const job = claim.discovery as Record<string, unknown>;
  if (typeof job.id !== 'string' || !job.id || typeof job.base_url !== 'string' || typeof job.start_path !== 'string' || !safeDiscoveryPath(job.start_path) || typeof job.max_pages !== 'number' || !Number.isInteger(job.max_pages) || job.max_pages < 1 || job.max_pages > 5) throw new Error('Invalid discovery scope');
  let setup: Scenario | undefined;
  if (job.setup_scenario !== undefined && job.setup_scenario !== null) {
    if (typeof job.setup_scenario !== 'object' || (job.setup_scenario as Record<string, unknown>).approved !== true) throw new Error('Unapproved discovery setup');
    setup = parseScenario(job.setup_scenario);
  }
  return { discovery: { id: job.id, base_url: job.base_url, start_path: job.start_path, max_pages: job.max_pages, setup_scenario: setup }, lease_token: claim.lease_token };
}
