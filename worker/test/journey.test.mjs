import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createServer } from 'node:http';
import { mkdtemp, readFile, writeFile, rm, access } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { chromium } from 'playwright';
import { executeScenario } from '../src/execute.ts';
import { parseScenario } from '../src/contract.ts';
import { decimal } from '../src/journey.ts';

const nav = (path) => ({ action: 'navigate', path });
const phase = (value) => ({ action: 'phase', value });
const actor = (value) => ({ action: 'use_actor', value });
const fill = (label, value) => ({ action: 'fill', locator: { label }, value });
const secret = (label, secret_env) => ({ action: 'fill', locator: { label }, secret_env });
const click = (name) => ({ action: 'click', locator: { role: 'button', name } });
const visible = (name) => ({ action: 'assert_visible', locator: { role: 'heading', name } });
const login = (name) => [
  actor(name),
  nav('/login'),
  secret('Email', `QA_TEST_${name.toUpperCase()}_EMAIL`),
  secret('Password', 'QA_TEST_PASSWORD'),
  click('Sign in'),
  visible('Home')
];

test('one reviewed journey hands identifiers across isolated actors, verifies saved amounts, repeats with fresh data and always cleans up', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'qa-journey-proof-')),
    statePath = join(directory, 'state.json'),
    auditPath = join(directory, 'audit.jsonl'),
    hook = join(directory, 'fixture.mjs');
  await writeFile(
    hook,
    `import{writeFile,readFile,appendFile,rm}from'node:fs/promises';const f=process.env.QA_FIXTURE_STATE_FILE,a=f+'.audit',id=process.env.QA_RUN_ID;await appendFile(a,JSON.stringify({action:process.argv[2],id})+'\\n');if(process.argv[2]==='prepare'){await writeFile(f,JSON.stringify({users:[{id:'buyer_'+id,email:'buyer_'+id+'@example.test'},{id:'seller_'+id,email:'seller_'+id+'@example.test'}],requests:[],offers:[],trades:[]}));console.log(JSON.stringify({env:{QA_TEST_BUYER_EMAIL:'buyer_'+id+'@example.test',QA_TEST_SELLER_EMAIL:'seller_'+id+'@example.test',QA_TEST_PASSWORD:'artificial-password'}}));}else{await rm(f,{force:true});console.log('{}');}`
  );
  process.env.QA_FIXTURE_STATE_FILE = statePath;
  let forbiddenClicks = 0;
  const server = createServer(async (req, res) => {
    try {
      const state = JSON.parse(await readFile(statePath, 'utf8'));
      const sid = (req.headers.cookie ?? '').match(/sid=([^;]+)/)?.[1],
        user = state.users.find((u) => u.id === sid);
      const body = async () => {
        let text = '';
        for await (const chunk of req) text += chunk;
        return JSON.parse(text || '{}');
      };
      const json = (value) => {
        res.setHeader('content-type', 'application/json');
        res.end(JSON.stringify(value));
      };
      const html = (value) => {
        res.setHeader('content-type', 'text/html');
        res.end(value);
      };
      const save = () => writeFile(statePath, JSON.stringify(state));
      if (req.url === '/login' && req.method === 'GET')
        return html(
          "<form onsubmit=\"event.preventDefault();fetch('/login',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({email:document.querySelector('#email').value,password:document.querySelector('#password').value})}).then(()=>location.href='/home')\"><label for=\"email\">Email</label><input id=\"email\"><label for=\"password\">Password</label><input id=\"password\" type=\"password\"><button>Sign in</button></form><script>setTimeout(()=>document.querySelector('#email').value='',100)</script>"
        );
      if (req.url === '/login' && req.method === 'POST') {
        const input = await body(),
          u = state.users.find((u) => u.email === input.email);
        assert.ok(u);
        assert.equal(input.password, 'artificial-password');
        res.setHeader('set-cookie', `sid=${u.id};HttpOnly;Secure;Path=/;SameSite=Lax`);
        return json({ ok: true });
      }
      if (!user) {
        res.statusCode = 401;
        return json({ error: 'not signed in' });
      }
      if (req.url === '/api/me') return json({ id: user.id });
      if (req.url === '/api/precision') {
        res.setHeader('content-type', 'application/json');
        res.end('{"amount":1000.0000000000000001}');
        return;
      }
      if (req.url === '/home') return html('<h1>Home</h1>');
      if (req.url === '/requests/new')
        return html(
          "<form onsubmit=\"event.preventDefault();fetch('/requests',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({amount:document.querySelector('#amount').value})}).then(r=>r.json()).then(r=>location.href='/requests/'+r.id)\"><label for=\"amount\">Amount</label><input id=\"amount\"><button>Publish</button></form>"
        );
      if (req.url === '/requests' && req.method === 'POST') {
        await new Promise((resolve) => setTimeout(resolve, 150));
        const input = await body(),
          r = { id: randomUUID(), owner_id: user.id, amount: input.amount, status: 'open' };
        state.requests.push(r);
        await save();
        return json(r);
      }
      if (req.url.startsWith('/api/requests/'))
        return json(state.requests.find((r) => r.id === req.url.split('/').at(-1)));
      if (req.url.startsWith('/requests/')) {
        const id = req.url.split('/').at(-1);
        return html(
          `<h1>Request</h1><button onclick="fetch('/offers',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({request_id:'${id}'})}).then(r=>r.json()).then(r=>location.href='/offers/'+r.id)">Offer</button>`
        );
      }
      if (req.url === '/offers' && req.method === 'POST') {
        const input = await body(),
          o = { id: randomUUID(), request_id: input.request_id, seller_id: user.id };
        state.offers.push(o);
        await save();
        return json(o);
      }
      if (req.url.startsWith('/offers/')) {
        const id = req.url.split('/').at(-1);
        return html(
          `<h1>Offer</h1><button onclick="fetch('/accept',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({offer_id:'${id}'})}).then(r=>r.json()).then(r=>location.href='/trades/'+r.id)">Accept</button>`
        );
      }
      if (req.url === '/accept' && req.method === 'POST') {
        const input = await body(),
          o = state.offers.find((o) => o.id === input.offer_id),
          r = state.requests.find((r) => r.id === o.request_id);
        assert.equal(user.id, r.owner_id);
        const t = {
          id: randomUUID(),
          request_id: r.id,
          offer_id: o.id,
          owner_id: user.id,
          seller_id: o.seller_id,
          amount: r.amount,
          to_amount: '1555000.00000000',
          status: 'terms_locked'
        };
        state.trades.push(t);
        await save();
        return json(t);
      }
      if (req.url.startsWith('/api/trades/'))
        return json(state.trades.find((t) => t.id === req.url.split('/').at(-1)));
      if (req.url.startsWith('/trades/'))
        return html(
          '<h1>Trade record</h1><button onclick="fetch(\'/forbidden\')">Must not run</button>'
        );
      if (req.url === '/forbidden') {
        forbiddenClicks++;
        return json({ ok: true });
      }
      if (req.url === '/slow') {
        return;
      }
      res.statusCode = 404;
      json({ error: 'missing' });
    } catch {
      res.statusCode = 500;
      res.end('fixture request failed');
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`,
    browser = await chromium.launch(),
    profiles = [
      {
        id: 'local_test',
        name: 'Local test',
        description: 'Isolated fixture',
        origins: [origin],
        secret_refs: ['QA_TEST_BUYER_EMAIL', 'QA_TEST_SELLER_EMAIL', 'QA_TEST_PASSWORD'],
        prepare: [process.execPath, hook, 'prepare'],
        cleanup: [process.execPath, hook, 'cleanup'],
        environment: ['QA_FIXTURE_STATE_FILE']
      }
    ];
  const steps = [
    { action: 'prepare_fixture', value: 'local_test' },
    phase('Create request'),
    ...login('buyer'),
    { action: 'capture_json', path: '/api/me', json_pointer: '/id', save_as: 'buyer_id' },
    nav('/requests/new'),
    fill('Amount', '1000'),
    click('Publish'),
    { action: 'capture_id', path: '/requests/', save_as: 'request_id' },
    {
      action: 'assert_json',
      path: '/api/requests/{{request_id}}',
      json_pointer: '/owner_id',
      value_ref: 'buyer_id'
    },
    phase('Make offer'),
    ...login('seller'),
    { action: 'capture_json', path: '/api/me', json_pointer: '/id', save_as: 'seller_id' },
    nav('/requests/{{request_id}}'),
    click('Offer'),
    { action: 'capture_id', path: '/offers/', save_as: 'offer_id' },
    phase('Accept trade'),
    actor('buyer'),
    nav('/offers/{{offer_id}}'),
    click('Accept'),
    { action: 'capture_id', path: '/trades/', save_as: 'trade_id' },
    visible('Trade record'),
    {
      action: 'assert_json',
      path: '/api/trades/{{trade_id}}',
      json_pointer: '/status',
      value: 'terms_locked'
    },
    {
      action: 'assert_json',
      path: '/api/trades/{{trade_id}}',
      json_pointer: '/owner_id',
      value_ref: 'buyer_id'
    },
    {
      action: 'assert_json',
      path: '/api/trades/{{trade_id}}',
      json_pointer: '/seller_id',
      value_ref: 'seller_id'
    },
    {
      action: 'assert_json_number',
      path: '/api/trades/{{trade_id}}',
      json_pointer: '/amount',
      value: '1000.00'
    },
    {
      action: 'assert_json_number',
      path: '/api/trades/{{trade_id}}',
      json_pointer: '/to_amount',
      value: '1555000'
    },
    { action: 'cleanup_fixture', value: 'local_test' }
  ];
  const snapshots = [];
  const execute = (id, definition = steps, signal = new AbortController().signal, p = profiles) =>
    executeScenario(
      browser,
      { id, base_url: origin },
      parseScenario({ id: 'whole-journey', steps: definition }),
      new Set([origin]),
      directory,
      signal,
      p,
      async (result) => {
        snapshots.push(result);
      }
    );
  try {
    for (const id of ['first', 'repeat']) {
      const result = await execute(id);
      assert.equal(result.status, 'passed', JSON.stringify(result.step_results));
      assert.equal(result.step_results.length, steps.length);
      assert.equal(result.artifacts.filter((a) => a.kind === 'video').length, 2);
      assert.deepEqual(
        result.artifacts.filter((a) => a.kind === 'video').map((a) => a.actor),
        ['buyer', 'seller']
      );
      await assert.rejects(access(statePath));
      assert.ok(!JSON.stringify(result).includes('artificial-password'));
    }
    const precision = await execute('precision', [
      steps[0],
      phase('Exact API number'),
      actor('buyer'),
      nav('/login'),
      secret('Email', 'QA_TEST_BUYER_EMAIL'),
      secret('Password', 'QA_TEST_PASSWORD'),
      click('Sign in'),
      visible('Home'),
      {
        action: 'assert_json_number',
        path: '/api/precision',
        json_pointer: '/amount',
        value: '1000'
      },
      steps.at(-1)
    ]);
    assert.equal(
      precision.status,
      'failed',
      'rounded JSON numbers must not falsely pass an amount check'
    );
    await assert.rejects(access(statePath));
    const wrong = steps.map((s) =>
      s.action === 'assert_json_number' && s.json_pointer === '/to_amount'
        ? { ...s, value: '1554999' }
        : s
    );
    wrong.splice(-1, 0, click('Must not run'));
    const failed = await execute('wrong-money', wrong);
    assert.equal(failed.status, 'failed');
    assert.equal(failed.step_results.at(-2).status, 'not_run');
    assert.equal(failed.step_results.at(-1).status, 'passed');
    assert.equal(forbiddenClicks, 0);
    await assert.rejects(access(statePath));
    const controller = new AbortController();
    setTimeout(() => controller.abort(), 1000);
    const cancelled = await execute(
      'cancelled',
      [steps[0], phase('Cancellation'), nav('/slow'), visible('never'), steps.at(-1)],
      controller.signal
    );
    assert.equal(cancelled.status, 'error');
    assert.equal(cancelled.step_results.at(-1).status, 'passed');
    await assert.rejects(access(statePath));
    const audit = (await readFile(statePath + '.audit', 'utf8')).trim().split('\n').map(JSON.parse);
    assert.deepEqual(
      audit.map((x) => x.action),
      Array.from({ length: 5 }, () => ['prepare', 'cleanup']).flat()
    );
    assert.notEqual(audit[0].id, audit[2].id);
    assert.ok(snapshots.some((s) => s.status === 'running'));
    const blocked = await execute('missing-profile', steps, new AbortController().signal, []);
    assert.equal(blocked.status, 'blocked');
    assert.equal((await readFile(statePath + '.audit', 'utf8')).trim().split('\n').length, 10);
    const badCleanup = await execute('cleanup-failure', steps, new AbortController().signal, [
      { ...profiles[0], cleanup: [process.execPath, '-e', 'process.exit(1)'] }
    ]);
    assert.equal(badCleanup.status, 'error');
    assert.equal(badCleanup.step_results.at(-1).status, 'error');
    await access(statePath);
  } finally {
    await browser.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    delete process.env.QA_FIXTURE_STATE_FILE;
    await rm(directory, { recursive: true, force: true });
  }
});

test('decimal comparisons preserve financial precision', () => {
  assert.equal(decimal('001000.00000000'), decimal('1000'));
  assert.notEqual(decimal('9007199254740993.01'), decimal('9007199254740993.02'));
  assert.throws(() => decimal('NaN'));
  assert.throws(() => decimal(9007199254740992));
});
