import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createServer } from 'node:http';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from 'playwright';
import { executeScenario } from '../src/execute.ts';
import { discover } from '../src/discover.ts';

test('real browser discovers and executes named controls without test IDs; ambiguous actions never click', async () => {
 let clicks = 0;
 const server = createServer((req,res) => {
  if(req.url === '/clicked') { clicks++; res.end('done'); return; }
  res.setHeader('content-type','text/html');
  res.end('<h1>Account access</h1><label for="email">Email address</label><input id="email"><label for="password">Password</label><input id="password" type="password"><button onclick="document.querySelector(\'h1\').textContent=\'Signed in\'">Sign in</button><button onclick="fetch(\'/clicked\')">Duplicate</button><button onclick="fetch(\'/clicked\')">Duplicate</button>');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base = `http://127.0.0.1:${server.address().port}`;
 const browser = await chromium.launch();
 const root = await mkdtemp(join(tmpdir(),'qa-accessible-'));
 try {
  const result = await discover(browser,{base_url:base,start_path:'/',max_pages:1},new Set([base]),new AbortController().signal);
  const elements = result.pages[0].elements;
  assert.ok(elements.some(e=>e.locator?.label==='Email address'));
  assert.ok(elements.some(e=>e.locator?.label==='Password'));
  assert.ok(elements.some(e=>e.locator?.role==='button'&&e.locator?.name==='Sign in'));
  assert.ok(elements.some(e=>e.locator?.role==='heading'&&e.locator?.name==='Account access'));
  assert.ok(elements.filter(e=>e.text==='Duplicate').every(e=>!e.locator));
  const steps = [
   {action:'navigate',path:'/'},
   {action:'fill',locator:{label:'Email address'},value:'synthetic@example.test'},
   {action:'fill',locator:{label:'Password'},value:'synthetic-password'},
   {action:'click',locator:{role:'button',name:'Sign in'}},
   {action:'assert_text',locator:{role:'heading',name:'Signed in'},value:'Signed in'},
  ];
  const run = (id,scenarioSteps)=>executeScenario(browser,{id,base_url:base},{id:'named',steps:scenarioSteps},new Set([base]),root,new AbortController().signal);
  const passed = await run('success',steps);
  assert.equal(passed.status,'passed');
  const failed = await run('wrong-text',[...steps.slice(0,-1),{...steps.at(-1),value:'Incorrect expectation'}]);
  assert.equal(failed.status,'failed');
  assert.equal(failed.step_results.at(-1).observed,'Signed in');
  const ambiguous = await run('ambiguous',[steps[0],{action:'click',locator:{role:'button',name:'Duplicate'}},steps.at(-1)]);
  assert.equal(ambiguous.status,'error');
  assert.equal(clicks,0);
 } finally {
  await browser.close(); await new Promise(r=>server.close(r)); await rm(root,{recursive:true,force:true});
 }
});
