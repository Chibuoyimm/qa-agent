// External generation is synthetic. Draft persistence, approval rejection, and
// healthy/faulty execution use the real API, PostgreSQL, and browser worker.
import assert from 'node:assert/strict';
import { resolve } from 'node:path';

export async function verifyProvidersUI(page, api, healthyID, faultyID, scenario, waitRun, evidence) {
  await page.getByRole('button', { name: 'Close AI proposals' }).click();
  await page.unroute('**/api/ai/config');
  await page.unroute('**/proposals');
  const providers = [
    { provider: 'openai', models: ['openai-model'], managed_available: false, byok_available: true },
    { provider: 'anthropic', models: ['claude-model', 'claude-second'], managed_available: false, byok_available: true },
    { provider: 'google', models: ['gemini-model'], managed_available: false, byok_available: true },
  ];
  await page.route('**/api/ai/config', route => route.fulfill({ json: { ...providers[0], providers, chatgpt: { enabled: true, active_profile_id: '', profiles: [] } } }));
  let calls = 0, quota = false;
  const drafts = [];
  await page.route('**/proposals', async route => {
    calls++;
    const input = route.request().postDataJSON();
    assert.ok(['anthropic', 'google'].includes(input.provider));
    assert.equal(input.credential_mode, 'byok');
    assert.equal(input.consent, true);
    assert.equal(route.request().headers()['x-qa-provider-key'], 'synthetic-key');
    assert.equal(JSON.stringify(input).includes('synthetic-key'), false);
    assert.equal(route.request().headers()['x-qa-anthropic-workspace'], input.provider === 'anthropic' ? 'wrkspc_fixture' : undefined);
    if (quota) { await route.fulfill({ status: 429, json: { error: 'provider quota or rate limit reached' } }); return; }
    const draft = { ...scenario, name: `${input.provider} proposed independent check`, approved: false };
    drafts.push(draft);
    await route.fulfill({ json: { provider: input.provider, model: input.model, credential_mode: 'byok', context_sha256: 'synthetic-context', scenarios: [draft], questions: [], assumptions: [] } });
  });
  await page.getByRole('button', { name: 'Ask AI to propose', exact: true }).click();
  await page.getByLabel('Provider', { exact: true }).selectOption('anthropic');
  await page.getByRole('textbox', { name: /^Testing request/ }).fill('Check revenue against the independent fixture.');
  await page.getByRole('textbox', { name: /^Application context/ }).fill('Paid 150000 minus refund 10000 equals 140000; cancelled 90000 is excluded.');
  const consent = page.getByRole('checkbox', { name: /I (agree to send|have reviewed)/ });
  const key = page.getByLabel('Anthropic API key', { exact: true });
  await key.fill('synthetic-key');
  await page.getByLabel(/^Anthropic workspace ID/).fill('wrkspc_fixture');
  await consent.check();
  await page.getByLabel('Model', { exact: true }).selectOption('claude-second');
  assert.equal(await consent.isChecked(), false);
  await consent.check();
  await page.getByLabel('Provider', { exact: true }).selectOption('google');
  assert.equal(await consent.isChecked(), false);
  assert.equal(await page.getByLabel('Google Gemini API key', { exact: true }).inputValue(), '');
  assert.equal(await page.getByText('ChatGPT subscription', { exact: true }).count(), 0);
  await page.getByLabel('Provider', { exact: true }).selectOption('anthropic');
  assert.equal(await page.getByLabel(/^Anthropic workspace ID/).inputValue(), '');
  for (const provider of ['anthropic', 'google']) {
    await page.getByLabel('Provider', { exact: true }).selectOption(provider);
    const label = provider === 'anthropic' ? 'Anthropic' : 'Google Gemini';
    await page.getByLabel(`${label} API key`, { exact: true }).fill('synthetic-key');
    if (provider === 'anthropic') await page.getByLabel(/^Anthropic workspace ID/).fill('wrkspc_fixture');
    await page.getByText(`I agree to send my testing request and application context to ${label} to generate proposals.`, { exact: true }).waitFor();
    await consent.check();
    if (provider === 'google') {
      quota = true;
      const before = calls;
      await page.getByRole('button', { name: 'Generate proposals', exact: true }).click();
      await page.getByText('provider quota or rate limit reached', { exact: true }).waitFor();
      assert.equal(calls, before + 1, 'Provider failure never switches provider or retries');
      quota = false;
      await page.getByLabel(`${label} API key`, { exact: true }).fill('synthetic-key');
      await consent.check();
    }
    await page.getByRole('button', { name: 'Generate proposals', exact: true }).click();
    await page.getByRole('heading', { name: `${provider} proposed independent check`, exact: true }).waitFor();
    assert.equal(await page.getByLabel(`${label} API key`, { exact: true }).inputValue(), '');
    if (provider === 'anthropic') assert.equal(await page.getByLabel(/^Anthropic workspace ID/).inputValue(), '');
    await page.locator('.ai-form-grid').screenshot({ path: resolve(evidence, `${provider}-provider-desktop.png`) });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
    if (provider === 'anthropic') {
      const field = await page.getByLabel(/^Anthropic workspace ID/).boundingBox();
      const help = await page.locator('.ai-workspace-note').boundingBox();
      const keyNote = await page.locator('.ai-settings > .ai-key-note').boundingBox();
      assert.ok(field && help && keyNote && field.y + field.height <= help.y && help.y + help.height <= keyNote.y, 'Workspace input and helper notes must not overlap');
    }
    await page.locator('.ai-settings').screenshot({ path: resolve(evidence, `${provider}-provider-mobile.png`) });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole('button', { name: 'Review in editor', exact: true }).click();
    assert.equal(await page.getByRole('checkbox', { name: 'Approve this scenario for runs', exact: true }).isChecked(), false);
    await page.getByRole('button', { name: 'Create scenario', exact: true }).click();
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    const saved = (await api(`/api/projects/${healthyID}/scenarios`)).find(item => item.name === `${provider} proposed independent check`);
    assert.equal(saved.approved, false);
    await api(`/api/projects/${healthyID}/runs`, { scenario_ids: [saved.id], mode: 'blocking' }, { status: 409 });
    // Explicitly compare the returned draft against independently reviewed fixture
    // requirements before constructing approved copies for actual execution.
    assert.deepEqual(saved.steps, scenario.steps);
    assert.equal(saved.expected_outcome, scenario.expected_outcome);
    for (const [projectID, expected] of [[healthyID, 'passed'], [faultyID, 'failed']]) {
      const approved = await api(`/api/projects/${projectID}/scenarios`, { ...drafts.at(-1), name: `${provider} reviewed ${expected} check`, approved: true });
      const queued = await api(`/api/projects/${projectID}/runs`, { scenario_ids: [approved.id], mode: 'blocking' });
      const run = await waitRun(queued.id);
      assert.equal(run.status, expected, JSON.stringify(run.results));
      assert.equal(run.gate, expected === 'passed' ? 'pass' : 'fail');
    }
    if (provider === 'anthropic') {
      await page.getByLabel('Provider', { exact: true }).selectOption('google');
      assert.equal(await page.locator('.ai-results').getByRole('heading', { name: `${provider} proposed independent check`, exact: true }).count(), 0, 'Provider change removes stale drafts');
    }
  }
  await page.getByRole('textbox', { name: /^Application context/ }).fill('Changed requirements must be reviewed again.');
  assert.equal(await consent.isChecked(), false);
  assert.equal(await page.locator('.ai-results').getByRole('heading', { name: 'google proposed independent check', exact: true }).count(), 0);
  assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0);
  assert.equal(calls, 3);
  return { name: 'Anthropic/Gemini provider and model selection, recipient consent, transient credentials, quota without fallback, unapproved persistence and healthy/faulty reviewed execution (synthetic generation)', status: 'passed' };
}
