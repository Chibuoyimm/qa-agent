import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { chromium } from 'playwright';
import { discover } from '../src/discover.ts';

let browser, server, origin;
let writes = 0, logoutVisits = 0;
before(async () => {
  browser = await chromium.launch();
  server = createServer((req, res) => {
    res.setHeader('content-type', 'text/html');
    if (req.method === 'POST') { writes++; res.end('saved'); return; }
    if (req.url === '/logout') { logoutVisits++; res.end('signed out'); return; }
    if (req.url === '/write') { res.end('<h1>Write</h1><script>fetch("/mutate", {method:"POST"})</script>'); return; }
    if (req.url === '/next') { res.end('<h1>Orders</h1><div data-testid="revenue">230000</div><a href="/third">Third</a>'); return; }
    if (req.url === '/third') { res.end('<h1>Third</h1>'); return; }
    res.end('<h1>Dashboard</h1><input data-testid="password" type="password" value="never-export"><input type="hidden" value="never-export-hidden"><button data-testid="save">Save</button><a href="/next">Orders</a><a href="/logout">Sign out</a><a href="/?token=sensitive">Secret query</a><a href="https://example.test/">External</a>');
  }).listen(0, '127.0.0.1');
  await once(server, 'listening'); origin = `http://127.0.0.1:${server.address().port}`;
});
after(async () => { await browser?.close(); if (server) await new Promise(r => server.close(r)); });
const run = (extra = {}) => discover(browser, { base_url: origin, start_path: '/', max_pages: 2, ...extra }, new Set([origin]), new AbortController().signal);

test('discovery follows bounded links and exposes observations without making assertions', async () => {
  const result = await run();
  assert.deepEqual(result.pages.map(p => p.path), ['/', '/next']);
  assert.equal(result.limited, true);
  assert.ok(result.pages[1].elements.some(e => e.test_id === 'revenue' && e.text === '230000'));
  assert.ok(result.pages[0].elements.some(e => e.test_id === 'save' && e.tag === 'button'));
  const serialized = JSON.stringify(result);
  assert.ok(!serialized.includes('never-export'));
  assert.ok(!serialized.includes('token=sensitive'));
  assert.equal(logoutVisits, 0);
  assert.ok(result.warnings.length > 0);
  assert.equal(browser.contexts().length, 0);
});

test('discovery blocks write requests before reaching the target', async () => {
  writes = 0;
  await assert.rejects(run({ start_path: '/write' }), /blocked a non-read request/);
  assert.equal(writes, 0);
  assert.equal(browser.contexts().length, 0);
});

test('discovery validates scope and preflights credentials before navigation', async () => {
  await assert.rejects(run({ start_path: '//elsewhere' }), /Invalid discovery scope/);
  await assert.rejects(run({ max_pages: 6 }), /Invalid discovery scope/);
  await assert.rejects(run({ setup_scenario: { id: 'setup', steps: [{ action: 'fill', test_id: 'password', secret_env: 'QA_TEST_DISCOVERY_MISSING' }, { action: 'assert_visible', test_id: 'save' }] } }), /secret is unavailable/);
  assert.equal(browser.contexts().length, 0);
});

test('cancelled discovery closes its browser context', async () => {
  const controller = new AbortController(); controller.abort();
  await assert.rejects(discover(browser, { base_url: origin, start_path: '/', max_pages: 1 }, new Set([origin]), controller.signal));
  assert.equal(browser.contexts().length, 0);
});

 test('discovery rejects non-test environment references at its worker boundary', async () => {
  process.env.QA_DISCOVERY_INTERNAL_TOKEN = 'must-not-be-filled';
  try {
    await assert.rejects(run({ setup_scenario: { id: 'setup', steps: [
      { action: 'navigate', path: '/' },
      { action: 'fill', test_id: 'password', secret_env: 'QA_DISCOVERY_INTERNAL_TOKEN' },
      { action: 'assert_visible', test_id: 'save' },
    ] } }), /Invalid fill value/);
    assert.equal(browser.contexts().length, 0);
  } finally { delete process.env.QA_DISCOVERY_INTERNAL_TOKEN; }
});

test('short setup secrets do not corrupt structured discovery output', async () => {
  process.env.QA_TEST_DISCOVERY_CODE = '1';
  try {
    const result = await run({ setup_scenario: { id: 'setup', steps: [
      { action: 'navigate', path: '/' },
      { action: 'fill', test_id: 'password', secret_env: 'QA_TEST_DISCOVERY_CODE' },
      { action: 'assert_visible', test_id: 'save' },
    ] } });
    assert.equal(result.pages[0].path, '/');
    assert.ok(Array.isArray(result.pages[0].elements));
    assert.equal(browser.contexts().length, 0);
  } finally { delete process.env.QA_TEST_DISCOVERY_CODE; }
});
