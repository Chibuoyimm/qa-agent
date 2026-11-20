// Invoked by TestWholeApp with an isolated database, Git and model fixtures, and a real worker.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import { chromium } from '../worker/node_modules/playwright/index.mjs';

const [base, target, artifactDir] = process.argv.slice(2);
const workerDir = fileURLToPath(new URL('../worker/', import.meta.url));
const browser = await chromium.launch();
const worker = spawn(process.execPath, ['--import','tsx','src/index.ts'], {cwd:workerDir,
  env:{...process.env, QA_API_BASE_URL:base, QA_WORKER_TOKEN:'worker', QA_ALLOWED_ORIGINS:target, QA_ARTIFACT_DIR:artifactDir, QA_FIXTURE_CONFIG:''},
  stdio:['ignore','inherit','inherit'],
});
const exited = once(worker,'exit');
const page = await browser.newPage();
page.setDefaultTimeout(15000);
const errors = [];
page.on('pageerror', err=>errors.push(err.message));
try {
  const response = await fetch(`${base}/api/projects`,{method:'POST',headers:{authorization:'Bearer operator','content-type':'application/json'},body:JSON.stringify({name:'Browser checkout review',base_url:target})});
  assert.ok(response.ok);
  const {id:project} = await response.json();
  await page.goto(`${base}/#/test?project=${project}`);
  await page.getByLabel('Workspace access token',{exact:true}).fill('operator');
  await page.getByRole('button',{name:'Open workspace',exact:true}).click();
  const prompt = 'test the whole app end to end';
  const message = page.getByRole('textbox',{name:'Your message',exact:true});
  const consent = page.getByRole('checkbox',{name:/I agree to send this request, project repository file lists/});
  const draft = page.getByRole('button',{name:'Draft test plan',exact:true});
  await message.fill(prompt);
  assert.ok(await page.getByLabel('Discover journeys across all project repositories').isChecked());
  await consent.check();
  await draft.click();
  await page.getByRole('button',{name:'Connect a repository',exact:true}).click();
  const connect = page.getByRole('button',{name:'Connect repository',exact:true});
  await page.getByLabel('GitHub repository').fill('shop/backend');
  await page.getByLabel('Role',{exact:false}).selectOption('backend');
  await connect.click();
  await page.getByText(/Connected shop\/backend/).waitFor();
  await page.getByLabel('GitHub repository').fill('shop/frontend');
  await page.getByLabel('Role',{exact:false}).selectOption('frontend');
  await page.getByLabel('GitHub token').fill('test-private-read-credential');
  await page.getByLabel('Save read access for future scans').check();
  await connect.click();
  await page.getByRole('button',{name:'Forget saved access for shop/frontend'}).waitFor();
  await page.getByRole('button',{name:'Return to test request'}).click();
  assert.equal(await message.inputValue(),prompt);
  await consent.check();
  await draft.click();
  const review = page.getByRole('region',{name:'Whole-app coverage'});
  await review.getByText(/2 project repositories · 2 ready for review/).waitFor();
  await review.getByRole('button', {name:'1 needs setup'}).click();
  const setup = page.getByRole('region', {name:'Needs setup (1)'});
  await setup.getByText('Cancel a guest order', {exact:true}).waitFor();
  await setup.getByText('Provide a pending guest order and its cancellation reference.').waitFor();
  assert.equal(await setup.getByRole('checkbox').count(),0);
  await setup.getByText('Source evidence',{exact:true}).click();
  assert.ok(await setup.getByRole('link',{name:'checkout.tsx'}).isVisible());
  await review.getByText('Repository evidence',{exact:true}).click();
  assert.ok((await review.innerText()).includes('shop/frontend · Order confirmation · checkout.tsx'));
  assert.ok((await review.innerText()).includes('shop/backend · Order confirmation · orders.ts'));
  assert.equal(await review.getByRole('link',{name:'checkout.tsx',exact:true}).first().getAttribute('href'), `https://github.com/shop/frontend/blob/${'a'.repeat(40)}/checkout.tsx`);
  await review.getByText(/Additional source findings/).click();
  assert.ok((await review.innerText()).includes('Guest cancellation'));
  await page.getByRole('button',{name:'Approve & run 2 checks',exact:true}).click();
  await page.getByText('2 passed · 0 failed · 0 unverified · 0 unfinished',{exact:true}).waitFor({timeout:60000});
  await page.getByRole('button',{name:'Open results and evidence',exact:true}).click();
  await page.getByRole('button',{name:'Discuss these results in Test'}).waitFor();
  const runID = new URLSearchParams(page.url().split('?')[1]).get('run');
  const run = await (await fetch(`${base}/api/runs/${runID}`,{headers:{authorization:'Bearer operator'}})).json();
  assert.equal(run.status,'passed');
  assert.equal(run.results.length,2);
  for (const result of run.results) {
    assert.equal(result.step_results.length,3);
    assert.ok(result.step_results.every(step=>step.status==='passed'));
  }
  assert.deepEqual(errors,[]);
} catch(err) {
  console.error((await page.locator('body').innerText()).slice(-4500));
  throw err;
} finally {
  await browser.close();
  worker.kill('SIGTERM');
  await exited;
}
