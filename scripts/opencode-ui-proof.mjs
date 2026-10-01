// External connection and generation use fixtures. Persistence, approval gates,
// and reviewed healthy/faulty runs use the real API, database, and browser worker.
import assert from 'node:assert/strict';
import { resolve } from 'node:path';

export async function verifyOpenCodeUI(page, api, healthyID, faultyID, scenario, waitRun, evidence) {
  await page.getByRole('button', { name: 'Close AI proposals' }).click();
  await page.unroute('**/api/ai/config');
  await page.unroute('**/proposals');
  await page.route('**/api/ai/config', route => route.fulfill({ json: { provider: 'openai', models: [], managed_available: false, byok_available: false, opencode_enabled: true } }));
  const models = [{ id: 'go-first', name: 'First Go model' }, { id: 'go-second', name: 'Second Go model' }];
  let connected = false, calls = 0, connects = 0, quota = false;
  const status = () => ({ enabled: true, connected, models: connected ? models : [] });
  await page.route('**/api/ai/opencode', route => route.fulfill({ json: status() }));
  await page.route('**/api/ai/opencode/connect', route => {
    connects++;
    assert.deepEqual(route.request().postDataJSON(), { key: 'synthetic-go-key' });
    connected = true;
    return route.fulfill({ json: status() });
  });
  await page.route('**/api/ai/opencode/disconnect', route => { connected = false; return route.fulfill({ json: status() }); });
  const draft = { ...scenario, name: 'OpenCode proposed independent check', approved: false };
  await page.route('**/proposals', route => {
    calls++;
    const input = route.request().postDataJSON();
    assert.equal(input.provider, 'opencode-go');
    assert.equal(input.credential_mode, 'opencode');
    assert.equal(input.model, 'go-second');
    assert.equal(input.consent, true);
    assert.equal(input.chatgpt_profile_id, undefined);
    assert.equal(route.request().headers()['x-qa-provider-key'], undefined);
    assert.equal(JSON.stringify(input).includes('synthetic-go-key'), false);
    return route.fulfill(quota ? { status: 429, json: { error: 'OpenCode Go usage or rate limit reached; check usage in the OpenCode console' } } : { json: { provider: 'opencode-go', model: input.model, credential_mode: 'opencode', context_sha256: 'synthetic-context', scenarios: [draft], questions: [], assumptions: [] } });
  });
  await page.getByRole('button', { name: 'Ask AI to propose', exact: true }).click();
  await page.getByLabel('Provider', { exact: true }).waitFor();
  assert.equal(await page.getByLabel('Provider', { exact: true }).inputValue(), 'opencode-go');
  await page.getByRole('button', { name: 'Connect OpenCode Go', exact: true }).waitFor();
  await page.getByLabel('OpenCode Go subscription key', { exact: true }).fill('synthetic-go-key');
  await page.getByRole('button', { name: 'Connect OpenCode Go', exact: true }).click();
  await page.getByText('Key connected', { exact: true }).waitFor();
  assert.equal(await page.getByLabel('OpenCode Go subscription key', { exact: true }).count(), 0);
  await page.getByRole('textbox', { name: /^Testing request/ }).fill('Check independent revenue requirements.');
  await page.getByRole('textbox', { name: /^Application context/ }).fill('Paid 150000 minus refund 10000 equals 140000; cancelled 90000 is excluded.');
  const consent = page.getByRole('checkbox', { name: /I (agree to send|have reviewed)/ });
  await page.getByLabel('Model', { exact: true }).selectOption('go-first');
  await consent.check();
  await page.getByLabel('Model', { exact: true }).selectOption('go-second');
  assert.equal(await consent.isChecked(), false);
  await consent.check();
  quota = true;
  await page.getByRole('button', { name: 'Generate proposals', exact: true }).click();
  await page.getByText('OpenCode Go usage or rate limit reached; check usage in the OpenCode console', { exact: true }).waitFor();
  assert.equal(calls, 1, 'UI must not retry or change subscription providers');
  quota = false;
  await page.getByRole('button', { name: 'Generate proposals', exact: true }).click();
  await page.getByRole('heading', { name: draft.name, exact: true }).waitFor();
  await page.locator('.ai-form-grid').screenshot({ path: resolve(evidence, 'opencode-provider-desktop.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
  await page.locator('.ai-settings').screenshot({ path: resolve(evidence, 'opencode-provider-mobile.png') });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole('button', { name: 'Review in editor', exact: true }).click();
  assert.equal(await page.getByRole('checkbox', { name: 'Approve this scenario for runs', exact: true }).isChecked(), false);
  await page.getByRole('button', { name: 'Create scenario', exact: true }).click();
  await page.getByRole('dialog').waitFor({ state: 'hidden' });
  const saved = (await api(`/api/projects/${healthyID}/scenarios`)).find(item => item.name === draft.name);
  assert.equal(saved.approved, false);
  await api(`/api/projects/${healthyID}/runs`, { scenario_ids: [saved.id], mode: 'blocking' }, { status: 409 });
  assert.deepEqual(saved.steps, scenario.steps);
  assert.equal(saved.expected_outcome, scenario.expected_outcome);
  for (const [id, expected] of [[healthyID, 'passed'], [faultyID, 'failed']]) {
    const reviewed = await api(`/api/projects/${id}/scenarios`, { ...draft, name: `OpenCode reviewed ${expected} check`, approved: true });
    const queued = await api(`/api/projects/${id}/runs`, { scenario_ids: [reviewed.id], mode: 'blocking' });
    assert.equal((await waitRun(queued.id)).status, expected);
  }
  await page.getByRole('button', { name: 'Disconnect OpenCode Go', exact: true }).click();
  await page.getByRole('button', { name: 'Connect OpenCode Go', exact: true }).waitFor();
  assert.equal(await page.getByLabel('OpenCode Go subscription key', { exact: true }).inputValue(), '');
  assert.equal(await consent.isChecked(), false);
  assert.equal(await page.locator('.ai-results').count(), 0);
  assert.equal(await page.getByRole('button', { name: 'Generate proposals', exact: true }).isDisabled(), true);
  assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0);
  assert.equal(connects, 1); assert.equal(calls, 2);
  return { name: 'OpenCode Go connection, models, recipient consent, quota, disconnect, unapproved persistence and healthy/faulty reviewed execution (synthetic generation)', status: 'passed' };
}
