import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, test } from 'node:test';
import { chromium } from 'playwright';
import { createSampleServer } from '../../sample/server.js';
import { executeScenario } from '../src/execute.ts';

let browser;
let healthy;
let faulty;
let external;
let artifacts;
let base;
let faultyBase;
let externalBase;
let blockedBase;
let blocked;
let blockedHits = 0;
let nestedHits = 0;
let iframeHopHits = 0;
let mutationPageHits = 0;
let mutationHits = 0;
const scenarios = JSON.parse(await readFile(new URL('../../sample/scenarios.json', import.meta.url), 'utf8'));

async function listen(server) {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return `http://127.0.0.1:${server.address().port}`;
}

before(async () => {
  process.env.QA_TEST_EMAIL = 'demo@example.test';
  process.env.QA_TEST_PASSWORD = 'pass1234';
  artifacts = await mkdtemp(join(tmpdir(), 'qa-worker-test-'));
  healthy = createSampleServer({ defect: false });
  faulty = createSampleServer({ defect: true });
  blocked = createServer((_, response) => { blockedHits++; response.writeHead(200); response.end('should never load'); });
  blockedBase = await listen(blocked);
  external = createServer((request, response) => {
    if (request.url === '/fetch') {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end(`<div data-testid="ready">ready</div><script>fetch('${blockedBase}/ping')</script>`);
    } else if (request.url === '/socket') {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end('<div data-testid="ready">ready</div><script>new WebSocket("ws://127.0.0.1:9/")</script>');
    } else if (request.url === '/popup') {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end(`<button data-testid="open" onclick="window.open('${blockedBase}/')">Open</button><div data-testid="ready">ready</div>`);
    } else if (request.url === '/iframe-host') {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end('<div data-testid="ready">ready</div><iframe src="/iframe-hop"></iframe>');
    } else if (request.url === '/iframe-hop') {
      iframeHopHits++;
      response.writeHead(302, { location: `${blockedBase}/` }); response.end();
    } else if (request.url === '/mutation-form') {
      mutationPageHits++;
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end('<form method="post" action="/mutate"><button data-testid="change">Change</button></form>');
    } else if (request.url === '/mutate') {
      mutationHits++;
      response.writeHead(200, { 'content-type': 'text/html' }); response.end('<div data-testid="changed">Changed</div>');
    } else if (request.url === '/start') {
      response.writeHead(302, { location: '/nested/page' }); response.end();
    } else if (request.url === '/nested/page') {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end('<div data-testid="final-url"></div><div data-testid="relative-data"></div><script>document.querySelector("[data-testid=final-url]").textContent=location.pathname;fetch("relative-data").then(r=>r.text()).then(t=>document.querySelector("[data-testid=relative-data]").textContent=t)</script>');
    } else if (request.url === '/nested/relative-data') {
      nestedHits++;
      response.writeHead(200); response.end('relative-ok');
    } else {
      response.writeHead(302, { location: request.url === '/hop' ? `${blockedBase}/` : '/hop' });
      response.end();
    }
  });
  [base, faultyBase, externalBase] = await Promise.all([listen(healthy), listen(faulty), listen(external)]);
  browser = await chromium.launch({ headless: true });
});

after(async () => {
  await browser?.close();
  await Promise.all([healthy, faulty, external, blocked].filter(Boolean).map(server => new Promise(resolve => server.close(resolve))));
  if (artifacts) await rm(artifacts, { recursive: true, force: true });
});

test('all approved sample journeys pass against the healthy fixture', async () => {
  for (const [index, scenario] of scenarios.entries()) {
    const result = await executeScenario(browser, { id: 'healthy', base_url: `${base}/` }, { ...scenario, id: `scenario-${index}` }, new Set([base]), artifacts, new AbortController().signal);
    assert.equal(result.status, 'passed', `${scenario.name}: ${result.message}`);
    assert.deepEqual(result.artifacts.map(a => a.kind).sort(), ['screenshot', 'video']);
    for (const artifact of result.artifacts) {
      assert.ok(!artifact.path.startsWith('/'));
      assert.ok(!artifact.path.includes('..'));
      assert.ok((await stat(join(artifacts, artifact.path))).size > 0);
    }
  }
});

test('wrong revenue fails even when UI and sample API agree', async () => {
  const result = await executeScenario(browser, { id: 'faulty', base_url: `${faultyBase}/` }, { ...scenarios[0], id: 'revenue' }, new Set([faultyBase]), artifacts, new AbortController().signal);
  assert.equal(result.status, 'failed');
  assert.match(result.message, /approved expected value/);
});

test('missing referenced secret blocks without disclosing a value', async () => {
  const old = process.env.QA_TEST_PASSWORD;
  delete process.env.QA_TEST_PASSWORD;
  try {
    const result = await executeScenario(browser, { id: 'blocked', base_url: `${base}/` }, { ...scenarios[0], id: 'secret' }, new Set([base]), artifacts, new AbortController().signal);
    assert.equal(result.status, 'blocked');
    assert.ok(!result.message.includes('pass1234'));
  } finally { process.env.QA_TEST_PASSWORD = old; }
});

test('redirect to a disallowed origin is an execution error', async () => {
  const scenario = { id: 'redirect', steps: [{ action: 'navigate', path: '/' }, { action: 'assert_visible', test_id: 'never-here' }] };
  const result = await executeScenario(browser, { id: 'redirect', base_url: `${externalBase}/` }, scenario, new Set([externalBase]), artifacts, new AbortController().signal);
  assert.equal(result.status, 'error');
  assert.match(result.message, /outside the worker allowlist/);
  assert.equal(blockedHits, 0, 'the disallowed server must receive no request');
});

test('safe redirect keeps its final URL and resolves relative resources normally', async () => {
  const scenario = { id: 'native-redirect', steps: [{ action: 'navigate', path: '/start' }, { action: 'assert_text', test_id: 'final-url', value: '/nested/page' }, { action: 'assert_text', test_id: 'relative-data', value: 'relative-ok' }] };
  const result = await executeScenario(browser, { id: 'native-redirect', base_url: `${externalBase}/` }, scenario, new Set([externalBase]), artifacts, new AbortController().signal);
  assert.equal(result.status, 'passed', result.message);
  assert.equal(nestedHits, 1);
});

test('page fetch to a disallowed origin is blocked before server contact', async () => {
  const scenario = { id: 'fetch', steps: [{ action: 'navigate', path: '/fetch' }, { action: 'assert_visible', test_id: 'ready' }] };
  const result = await executeScenario(browser, { id: 'fetch', base_url: `${externalBase}/` }, scenario, new Set([externalBase]), artifacts, new AbortController().signal);
  assert.equal(result.status, 'error');
  assert.equal(blockedHits, 0);
});

test('WebSocket requests are rejected before a connection', async () => {
  const scenario = { id: 'socket', steps: [{ action: 'navigate', path: '/socket' }, { action: 'assert_visible', test_id: 'ready' }] };
  const result = await executeScenario(browser, { id: 'socket', base_url: `${externalBase}/` }, scenario, new Set([externalBase]), artifacts, new AbortController().signal);
  assert.equal(result.status, 'error');
  assert.match(result.message, /WebSocket/);
});

test('popup attempts are rejected before reaching another origin', async () => {
  const scenario = { id: 'popup', steps: [{ action: 'navigate', path: '/popup' }, { action: 'click', test_id: 'open' }, { action: 'assert_visible', test_id: 'ready' }] };
  const result = await executeScenario(browser, { id: 'popup', base_url: `${externalBase}/` }, scenario, new Set([externalBase]), artifacts, new AbortController().signal);
  assert.equal(result.status, 'error');
  assert.match(result.message, /popup|allowlist/);
  assert.equal(blockedHits, 0);
});

test('iframe redirects cannot bypass the page CDP session', async () => {
  const scenario = { id: 'iframe', steps: [{ action: 'navigate', path: '/iframe-host' }, { action: 'assert_visible', test_id: 'ready' }] };
  const result = await executeScenario(browser, { id: 'iframe', base_url: `${externalBase}/` }, scenario, new Set([externalBase]), artifacts, new AbortController().signal);
  assert.equal(result.status, 'error');
  assert.match(result.message, /iframe/);
  assert.equal(iframeHopHits, 0, 'iframe first request must be blocked');
  assert.equal(blockedHits, 0, 'redirect destination must receive no request');
});

test('missing secret preflight prevents earlier state-changing steps', async () => {
  delete process.env.QA_TEST_MISSING_AFTER_CLICK;
  const scenario = { id: 'preflight', steps: [
    { action: 'navigate', path: '/mutation-form' },
    { action: 'click', test_id: 'change' },
    { action: 'fill', test_id: 'never-reached', secret_env: 'QA_TEST_MISSING_AFTER_CLICK' },
    { action: 'assert_visible', test_id: 'changed' },
  ] };
  const result = await executeScenario(browser, { id: 'preflight', base_url: `${externalBase}/` }, scenario, new Set([externalBase]), artifacts, new AbortController().signal);
  assert.equal(result.status, 'blocked');
  assert.equal(mutationPageHits, 0);
  assert.equal(mutationHits, 0);
  assert.deepEqual(result.artifacts, []);
});
