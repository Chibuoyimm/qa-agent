// Subscription UI boundaries use synthetic account/provider responses. Saving
// the reviewed scenario still uses the real API and PostgreSQL.
import assert from 'node:assert/strict';
import { resolve } from 'node:path';

export async function verifyChatGPTUI(page, api, projectID, scenario, evidence) {
  await page.getByRole('button', { name: 'Close AI proposals' }).click();
  await page.unroute('**/api/ai/config');
  await page.unroute('**/proposals');
  const profiles = [
    { id: 'synthetic-profile-a', email: 'qa@example.test', label: 'QA account A', connected: true, sharing: true },
    { id: 'synthetic-profile-b', email: 'qa@example.test', label: 'QA account B', connected: true, sharing: true },
  ];
  let status = { enabled: true, active_profile_id: '', profiles: [] };
  let nextLogin = 0, cancelled = 0, calls = 0;
  let quota = false;
  await page.route('**/api/ai/config', route => route.fulfill({ json: {
    provider: 'openai', models: [], managed_available: false, byok_available: false, chatgpt: status,
  } }));
  await page.route('**/api/ai/chatgpt', async route => {
    if (status.login?.status === 'pending' && nextLogin === 2) {
      status = { enabled: true, active_profile_id: profiles[0].id, profiles, login: { id: 'synthetic-login-2', status: 'completed', message: 'ChatGPT account connected.' } };
    }
    await route.fulfill({ json: status });
  });
  await page.route('**/api/ai/chatgpt/login', async route => {
    nextLogin++;
    status = { ...status, login: { id: `synthetic-login-${nextLogin}`, status: 'pending' } };
    await route.fulfill({ status: 202, json: { id: status.login.id, auth_url: 'https://auth.openai.com/api/accounts/authorize?fixture=qa-ui', expires_at: new Date(Date.now()+600000).toISOString() } });
  });
  await page.context().route('https://auth.openai.com/api/accounts/authorize?fixture=qa-ui', route => route.fulfill({ contentType: 'text/html', body: '<p>Synthetic sign-in fixture. No account credentials are requested.</p>' }));
  await page.route('**/api/ai/chatgpt/login/cancel', async route => {
    assert.equal(route.request().postDataJSON().login_id, status.login.id);
    cancelled++;
    status = { ...status, login: { ...status.login, status: 'cancelled' } };
    await route.fulfill({ json: status });
  });
  await page.route('**/api/ai/chatgpt/models?*', route => {
    const profileID = new URL(route.request().url()).searchParams.get('profile_id');
    assert.ok(profiles.some(profile => profile.id === profileID));
    return route.fulfill({ json: [
      { slug: 'subscription-model', display_name: 'Subscription model' },
      { slug: 'subscription-model-two', display_name: 'Second subscription model' },
    ] });
  });
  await page.route('**/api/ai/chatgpt/select', async route => {
    status = { ...status, active_profile_id: route.request().postDataJSON().profile_id };
    await route.fulfill({ json: status });
  });
  await page.route('**/api/ai/chatgpt/disconnect', async route => {
    const profileID = route.request().postDataJSON().profile_id;
    status = { ...status, active_profile_id: '', profiles: status.profiles.map(profile => profile.id === profileID ? { ...profile, connected: false, sharing: false } : profile) };
    await route.fulfill({ json: status });
  });
  const proposed = { ...scenario, name: 'Subscription proposed independent check', approved: false };
  await page.route('**/proposals', async route => {
    calls++;
    assert.equal(route.request().headers()['x-qa-provider-key'], undefined);
    const input = route.request().postDataJSON();
    assert.equal(input.credential_mode, 'chatgpt');
    assert.equal(input.chatgpt_profile_id, profiles[1].id);
    assert.equal(input.consent, true);
    if (quota) { await route.fulfill({ status: 429, json: { error: 'ChatGPT plan usage is unavailable or has reached its limit. Manage usage in ChatGPT settings' } }); return; }
    await route.fulfill({ json: { provider: 'openai', credential_mode: 'chatgpt', chatgpt_profile_id: profiles[1].id, model: input.model, context_sha256: 'synthetic-context', scenarios: [proposed], questions: [], assumptions: [] } });
  });
  await page.getByRole('button', { name: 'Ask AI to propose', exact: true }).click();
  const connect = page.getByRole('button', { name: /^(Continue with ChatGPT|Connect ChatGPT)$/ });
  await connect.waitFor();
  assert.equal(await page.getByRole('button', { name: 'Generate proposals', exact: true }).isEnabled(), false);
  await connect.click();
  await page.getByText(/Finish connecting in the browser tab/).waitFor();
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByText('ChatGPT connection cancelled.', { exact: true }).waitFor();
  assert.equal(cancelled, 1);
  await connect.click();
  await page.getByLabel('Account', { exact: true }).waitFor();
  await page.getByLabel('Model', { exact: true }).selectOption('subscription-model');
  await page.getByRole('textbox', { name: /^Testing request/ }).fill('Check independent fixture correctness.');
  await page.getByRole('textbox', { name: /^Application context/ }).fill('Paid 150000 minus refund 10000 equals 140000; cancelled orders are excluded.');
  const consent = page.getByRole('checkbox', { name: /I (agree to send|have reviewed)/ });
  await consent.check();
  await page.getByLabel('Model', { exact: true }).selectOption('subscription-model-two');
  assert.equal(await consent.isChecked(), false, 'Changing a model resets consent');
  await consent.check();
  await page.getByLabel('Account', { exact: true }).selectOption(profiles[1].id);
  assert.equal(await consent.isChecked(), false, 'Changing an account resets consent');
  const useAccount = page.getByRole('button', { name: 'Use this account', exact: true });
  if (await useAccount.isVisible()) await useAccount.click();
  await page.getByLabel('Model', { exact: true }).selectOption('subscription-model');
  await consent.check();
  quota = true;
  await page.getByRole('button', { name: 'Generate proposals', exact: true }).click();
  await page.getByText(/ChatGPT plan usage is unavailable or has reached its limit/).waitFor();
  assert.equal(calls, 1, 'A quota error does not trigger another provider request');
  quota = false;
  await page.getByRole('button', { name: 'Generate proposals', exact: true }).click();
  await page.getByRole('heading', { name: proposed.name, exact: true }).waitFor();
  assert.equal(calls, 2);
  await page.locator('.ai-form-grid').screenshot({ path: resolve(evidence, 'chatgpt-subscription-desktop.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'Subscription controls fit the mobile viewport');
  await page.locator('.ai-settings').screenshot({ path: resolve(evidence, 'chatgpt-subscription-mobile.png') });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole('button', { name: 'Review in editor', exact: true }).click();
  assert.equal(await page.getByRole('checkbox', { name: 'Approve this scenario for runs', exact: true }).isChecked(), false);
  await page.getByRole('button', { name: 'Create scenario', exact: true }).click();
  await page.getByRole('dialog').waitFor({ state: 'hidden' });
  const saved = await api(`/api/projects/${projectID}/scenarios`);
  assert.equal(saved.find(item => item.name === proposed.name)?.approved, false, 'Subscription draft is saved unapproved until explicitly reviewed');
  assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0, 'Connection and provider credentials never enter browser storage');
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
  await page.getByRole('button', { name: 'Reconnect account', exact: true }).waitFor();
  assert.equal(await page.getByRole('button', { name: 'Generate proposals', exact: true }).isEnabled(), false);
  return { name: 'ChatGPT subscription UI: connection/cancellation, account/model consent, quota handling, draft review and disconnect (synthetic external boundary, real persistence)', status: 'passed' };
}
