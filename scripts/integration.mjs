// Disposable PostgreSQL + real API + real Chromium. No customer accounts or model calls.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:net';
import { randomBytes, createHash } from 'node:crypto';
import { mkdir, readFile, writeFile, open } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { verifyChatGPTUI } from './chatgpt-ui-proof.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const key = `${Date.now()}-${randomBytes(3).toString('hex')}`;
const evidence = resolve(root, 'artifacts', `integration-${key}`);
await mkdir(evidence, { recursive: true });
const container = `qa-agent-proof-${key}`;
const children = [];
const files = [];
const observations = [];
let containerCreated = false;
const manifest = { started_at: new Date().toISOString(), container, processes: [], runs: [], cleanup: false };
const command = (cmd, args, options = {}) => {
  const result = spawnSync(cmd, args, { cwd: root, encoding: 'utf8', ...options });
  if (result.error || result.status !== 0) throw new Error(`${cmd} failed: ${result.stderr || result.error}`);
  return result.stdout.trim();
};
const port = async () => {
  const server = createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const selected = server.address().port; await new Promise(r => server.close(r)); return selected;
};
const start = async (name, cmd, args, env) => {
  const file = await open(resolve(evidence, `${name}.log`), 'w'); files.push(file);
  const process = spawn(cmd, args, { cwd: root, env: { ...globalThis.process.env, ...env }, stdio: ['ignore', file.fd, file.fd] });
  process.logPath = resolve(evidence, `${name}.log`);
  children.push(process); manifest.processes.push({ name, pid: process.pid });
  process.on('error', error => { process.launchError = error; });
  return process;
};
const stop = async process => {
  if (process.exitCode !== null || process.signalCode !== null) return;
  process.kill('SIGTERM');
  const timer = setTimeout(() => process.kill('SIGKILL'), 5000);
  try { await once(process, 'exit'); } finally { clearTimeout(timer); }
};
const ready = async (url, process) => {
  for (let i = 0; i < 100; i++) {
    if (process.launchError || process.exitCode !== null) {
      const log = await readFile(process.logPath, 'utf8');
      throw new Error(`Service exited before readiness: ${url}\n${log.slice(-4000)}`);
    }
    try { if ((await fetch(url, { signal: AbortSignal.timeout(500) })).ok) return; } catch {}
    await new Promise(r => setTimeout(r, 100));
  }
  throw new Error(`Service did not become ready: ${url}`);
};

try {
  command('go', ['build', '-o', resolve(evidence, 'server'), './cmd/server']);
  command('go', ['build', '-o', resolve(evidence, 'qa'), './cmd/qa']);
  command('docker', ['run', '--detach', '--name', container, '-e', 'POSTGRES_USER=qa', '-e', 'POSTGRES_PASSWORD=proof-only', '-e', 'POSTGRES_DB=qa_proof', '-p', '127.0.0.1::5432', 'postgres:17-alpine']);
  containerCreated = true;
  const mapping = command('docker', ['port', container, '5432/tcp']);
  const dbPort = mapping.split(':').at(-1);
  let dbReady = false;
  for (let i = 0; i < 100; i++) {
    // The image's temporary initialization server accepts Unix sockets before
    // the final server is listening on TCP. The API needs the latter.
    const result = spawnSync('docker', ['exec', container, 'pg_isready', '-h', '127.0.0.1', '-U', 'qa', '-d', 'qa_proof'], { stdio: 'ignore' });
    if (result.status === 0) { dbReady = true; break; }
    await new Promise(r => setTimeout(r, 200));
  }
  assert.ok(dbReady, 'Disposable PostgreSQL readiness');
  const [apiPort, goodPort, badPort] = await Promise.all([port(), port(), port()]);
  const base = `http://127.0.0.1:${apiPort}`;
  const goodURL = `http://127.0.0.1:${goodPort}`;
  const badURL = `http://127.0.0.1:${badPort}`;
  const apiToken = randomBytes(24).toString('hex'), workerToken = randomBytes(24).toString('hex');
  const env = {
    DATABASE_URL: `postgres://qa:proof-only@127.0.0.1:${dbPort}/qa_proof?sslmode=disable`,
    QA_API_TOKEN: apiToken, QA_WORKER_TOKEN: workerToken, QA_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
    QA_API_BASE_URL: base, QA_ALLOWED_ORIGINS: `${goodURL},${badURL}`, QA_ARTIFACT_DIR: resolve(evidence, 'browser'),
    QA_TEST_EMAIL: 'demo@example.test', QA_TEST_PASSWORD: 'pass1234', QA_WORKER_ID: 'integration-worker',
    QA_OPENAI_MODELS: '', QA_OPENAI_API_KEY: '', QA_CHATGPT_ENABLED: 'false',
  };
  let server = await start('api', resolve(evidence, 'server'), [], env);
  const good = await start('sample-healthy', process.execPath, ['sample/server.js'], { ...env, PORT: String(goodPort), QA_SAMPLE_DEFECT: '0' });
  const bad = await start('sample-faulty', process.execPath, ['sample/server.js'], { ...env, PORT: String(badPort), QA_SAMPLE_DEFECT: '1' });
  await Promise.all([ready(`${base}/healthz`, server), ready(`${goodURL}/healthz`, good), ready(`${badURL}/healthz`, bad)]);
  let worker = await start('worker', process.execPath, ['--import', './worker/node_modules/tsx/dist/loader.mjs', 'worker/src/index.ts'], env);
  const api = async (path, body, options = {}) => {
    const response = await fetch(`${base}${path}`, {
      method: body === undefined ? 'GET' : 'POST', redirect: 'error',
      headers: { authorization: `Bearer ${options.worker ? workerToken : apiToken}`, 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(options.timeout ?? 15000),
    });
    if (options.status) { assert.equal(response.status, options.status, path); return; }
    if (!response.ok) throw new Error(`${path}: HTTP ${response.status}: ${await response.text()}`);
    if (response.status === 204) return null;
    return response.json();
  };
  const templates = JSON.parse(await readFile(resolve(root, 'sample/scenarios.json'), 'utf8'));
  const setup = async (name, url) => {
    const project = await api('/api/projects', { name, base_url: url });
    const scenarios = [];
    for (const template of templates) scenarios.push(await api(`/api/projects/${project.id}/scenarios`, template));
    return { project, scenarios };
  };
  const healthy = await setup('Healthy correctness proof', goodURL);
  const faulty = await setup('Faulty correctness proof', badURL);
  let repositorySnapshot;
  if (process.env.QA_PROOF_GITHUB === '1') {
    repositorySnapshot = await api(`/api/projects/${healthy.project.id}/repositories/sync`, {
      repository: 'Chibuoyimm/qa-agent', ref: 'main', role: 'frontend', paths: ['sample/README.md', 'sample/scenarios.json'],
    }, { timeout: 60000 });
    assert.match(repositorySnapshot.commit_sha, /^[a-f0-9]{40}$/);
    assert.equal(repositorySnapshot.files.length, 2);
    assert.ok(repositorySnapshot.files[0].content.includes('Synthetic SaaS'));
    const readBack = await api(`/api/projects/${healthy.project.id}/repositories/${repositorySnapshot.id}`);
    assert.deepEqual(readBack, repositorySnapshot);
    await api(`/api/projects/${faulty.project.id}/repositories/${repositorySnapshot.id}`, undefined, { status: 404 });
    observations.push({ name: 'Live public GitHub import is commit-pinned, persisted, and project-scoped', status: 'passed', commit_sha: repositorySnapshot.commit_sha });
  }

  const waitRun = async id => {
    for (let i = 0; i < 600; i++) {
      const run = await api(`/api/runs/${id}`);
      if (!['queued', 'running'].includes(run.status)) return run;
      if (worker.exitCode !== null) throw new Error('Worker exited while waiting for run');
      await new Promise(r => setTimeout(r, 200));
    }
    throw new Error(`Run ${id} timed out`);
  };
  const runSuite = async (suite, mode, expected) => {
    const queued = await api(`/api/projects/${suite.project.id}/runs`, { scenario_ids: suite.scenarios.map(s => s.id), mode });
    manifest.runs.push(queued.id);
    const run = await waitRun(queued.id);
    assert.equal(run.status, expected, JSON.stringify(run.results));
    assert.equal(run.gate, expected === 'passed' ? 'pass' : mode === 'blocking' ? 'fail' : 'warn');
    assert.equal(run.results.length, suite.scenarios.length);
    await writeFile(resolve(evidence, `${queued.id}.json`), JSON.stringify(run, null, 2));
    observations.push({ name: suite.project.name, mode, status: run.status, gate: run.gate, run_id: run.id });
    return run;
  };
  const healthyRun = await runSuite(healthy, 'blocking', 'passed');
  const discovery = await api(`/api/projects/${healthy.project.id}/discoveries`, {
    start_path: '/dashboard', max_pages: 2, setup_scenario_id: healthy.scenarios[0].id,
  });
  let observed;
  for (let i = 0; i < 500; i++) {
    observed = await api(`/api/discoveries/${discovery.id}`);
    if (!['queued', 'running'].includes(observed.status)) break;
    await new Promise(r => setTimeout(r, 200));
  }
  assert.equal(observed.status, 'completed', JSON.stringify(observed));
  assert.ok(observed.result.pages.some(p => p.elements.some(e => e.test_id === 'dashboard-revenue')));
  assert.ok(!JSON.stringify(observed.result).includes(env.QA_TEST_PASSWORD));
  assert.ok(!JSON.stringify(observed.result).includes(env.QA_TEST_EMAIL));
  observations.push({ name: 'Approved setup and live browser discovery produce reviewable observations', status: 'passed' });

  await runSuite(faulty, 'blocking', 'failed');
  await runSuite(faulty, 'advisory', 'failed');
  const blocked = structuredClone(templates[0]);
  const secretStep = blocked.steps.find(s => s.secret_env);
  assert.ok(secretStep, 'Sample must include a secret reference');
  secretStep.secret_env = 'QA_TEST_INTENTIONALLY_MISSING';
  const blockedScenario = await api(`/api/projects/${healthy.project.id}/scenarios`, { ...blocked, name: 'Missing identity blocks a run' });
  await runSuite({ project: healthy.project, scenarios: [blockedScenario] }, 'blocking', 'blocked');
  const draft = await api(`/api/projects/${healthy.project.id}/scenarios`, { ...templates[0], approved: false });
  await api(`/api/projects/${healthy.project.id}/runs`, { scenario_ids: [draft.id], mode: 'blocking' }, { status: 409 });
  observations.push({ name: 'Unapproved scenario rejected', status: 'passed' });
  await stop(worker);
  const queued = await api(`/api/projects/${healthy.project.id}/runs`, { scenario_ids: [healthy.scenarios[0].id], mode: 'blocking' });
  const claim = await api('/api/worker/claim', { worker_id: 'proof-cancel' }, { worker: true });
  assert.equal(claim.run.id, queued.id);
  const cancelled = await api(`/api/runs/${queued.id}/cancel`, {});
  assert.equal(cancelled.status, 'cancelled'); assert.equal(cancelled.gate, 'fail');
  await api(`/api/worker/runs/${queued.id}/complete`, { lease_token: claim.lease_token, results: [] }, { worker: true, status: 409 });
  observations.push({ name: 'Cancellation rejects late completion', status: 'passed' });
  await stop(server);
  server = await start('api-restarted', resolve(evidence, 'server'), [], env);
  await ready(`${base}/healthz`, server);
  const persisted = await api(`/api/runs/${healthyRun.id}`);
  assert.deepEqual(persisted, healthyRun);
  observations.push({ name: 'Results survive API restart', status: 'passed' });
  assert.deepEqual(await api(`/api/discoveries/${discovery.id}`), observed);
  if (repositorySnapshot) assert.deepEqual(await api(`/api/projects/${healthy.project.id}/repositories/${repositorySnapshot.id}`), repositorySnapshot);

  // The CLI must observe the same gate policy against the real API.
  worker = await start('worker-cli', process.execPath, ['--import', './worker/node_modules/tsx/dist/loader.mjs', 'worker/src/index.ts'], env);
  const cli = spawn(resolve(evidence, 'qa'), ['run', '--project', faulty.project.id, '--scenarios', faulty.scenarios[0].id, '--mode', 'blocking', '--timeout', '60s'], { cwd: root, env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
  children.push(cli); manifest.processes.push({ name: 'cli', pid: cli.pid });
  let cliOutput = ''; cli.stdout.on('data', d => { cliOutput += d; }); cli.stderr.on('data', d => { cliOutput += d; });
  const [code] = await once(cli, 'exit');
  assert.equal(code, 1, cliOutput);
  await writeFile(resolve(evidence, 'cli.txt'), cliOutput);
  observations.push({ name: 'Pipeline CLI blocks faulty build', status: 'passed' });
  const discoveryCLI = spawn(resolve(evidence, 'qa'), ['discover', '--project', healthy.project.id, '--start-path', '/dashboard', '--setup-scenario', healthy.scenarios[0].id, '--max-pages', '2', '--timeout', '60s', '--json'], { cwd: root, env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
  children.push(discoveryCLI); manifest.processes.push({ name: 'discovery-cli', pid: discoveryCLI.pid });
  let discoveryJSON = '', discoveryProgress = '';
  discoveryCLI.stdout.on('data', d => { discoveryJSON += d; });
  discoveryCLI.stderr.on('data', d => { discoveryProgress += d; });
  const [discoveryCode] = await once(discoveryCLI, 'exit');
  assert.equal(discoveryCode, 0, discoveryProgress);
  assert.equal(JSON.parse(discoveryJSON).status, 'completed');
  observations.push({ name: 'Discovery CLI executes an authenticated browser job and emits JSON', status: 'passed' });

  const sourceRevision = repositorySnapshot?.commit_sha ?? command('git', ['rev-parse', 'HEAD']);
  const sourceSpecs = [
    { repository: 'Chibuoyimm/qa-agent', role: 'frontend', commit_sha: sourceRevision, paths: ['sample/README.md', 'sample/scenarios.json'] },
    { repository: 'Chibuoyimm/qa-agent', role: 'backend', commit_sha: sourceRevision, paths: ['sample/server.js'] },
  ];
  // Offline CI seeds only the external GitHub boundary. The release API,
  // database, worker, target browser, and CLI resume flow are all real.
  const seedSnapshot = async spec => {
    const id = randomBytes(16).toString('hex');
    const sourceFiles = await Promise.all(spec.paths.map(async path => ({ path, content: await readFile(resolve(root, path), 'utf8') })));
    const hash = createHash('sha256');
    for (const file of sourceFiles) hash.update(file.path).update('\0').update(file.content).update('\0');
    const bytes = sourceFiles.reduce((sum, file) => sum + Buffer.byteLength(file.content), 0);
    const literal = value => `'${String(value).replaceAll("'", "''")}'`;
    command('docker', ['exec', container, 'psql', '-U', 'qa', '-d', 'qa_proof', '-v', 'ON_ERROR_STOP=1', '-c',
      `INSERT INTO repository_snapshots(id,project_id,repository,ref,role,commit_sha,content_sha256,files,file_count,total_bytes) VALUES (${[id, healthy.project.id, spec.repository, spec.commit_sha, spec.role, spec.commit_sha, hash.digest('hex'), JSON.stringify(sourceFiles)].map(literal).join(',')},${sourceFiles.length},${bytes})`]);
    return { snapshot_id: id, repository: spec.repository, role: spec.role, commit_sha: spec.commit_sha };
  };
  const sourceInputs = process.env.QA_PROOF_GITHUB === '1' ? null : await Promise.all(sourceSpecs.map(seedSnapshot));
  const releaseCLI = async (name, manifestInput, expectedCode) => {
    const manifestPath = resolve(evidence, `${name}-manifest.json`);
    await writeFile(manifestPath, JSON.stringify(manifestInput, null, 2));
    const child = spawn(resolve(evidence, 'qa'), ['release', '--manifest', manifestPath, '--timeout', '120s', '--poll', '100ms', '--json'], { cwd: root, env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
    children.push(child); manifest.processes.push({ name, pid: child.pid });
    let stdout = '', stderr = '';
    child.stdout.on('data', data => { stdout += data; });
    child.stderr.on('data', data => { stderr += data; });
    const [status] = await once(child, 'exit');
    assert.equal(status, expectedCode, stderr);
    await writeFile(resolve(evidence, `${name}.json`), stdout);
    const release = JSON.parse(stdout);
    manifest.runs.push(release.run.id);
    return release;
  };
  const releaseManifest = (deploymentKey, url) => ({ project_id: healthy.project.id, deployment_key: deploymentKey,
    base_url: url, readiness_path: '/healthz', mode: 'blocking', scenario_ids: [healthy.scenarios[0].id], repositories: sourceSpecs });
  const releaseEndpoint = `/api/projects/${healthy.project.id}/releases`;
  const createRelease = async (deploymentKey, url) => {
    const input = releaseManifest(deploymentKey, url);
    if (sourceInputs) {
      const queuedRelease = await api(releaseEndpoint, { deployment_key: deploymentKey, base_url: url, mode: input.mode,
        scenario_ids: input.scenario_ids, repositories: sourceInputs });
      await waitRun(queuedRelease.run.id);
    }
    return releaseCLI(`release-${deploymentKey}`, input, url === goodURL ? 0 : 1);
  };
  const goodRelease = await createRelease('healthy-deployment', goodURL);
  const badRelease = await createRelease('faulty-deployment', badURL);
  assert.equal(goodRelease.run.status, 'passed');
  assert.equal(badRelease.run.status, 'failed');
  assert.equal(badRelease.run.gate, 'fail');
  assert.equal(badRelease.base_url, badURL);
  assert.equal(badRelease.run.base_url, badURL);
  assert.equal((await api('/api/projects')).find(project => project.id === healthy.project.id).base_url, goodURL);
  assert.deepEqual(goodRelease.repositories.map(source => source.role).sort(), ['backend', 'frontend']);
  const runsBeforeRetry = await api(`/api/projects/${healthy.project.id}/runs`);
  const snapshotsBeforeRetry = await api(`/api/projects/${healthy.project.id}/repositories`);
  const retriedRelease = await releaseCLI('release-retry', { ...releaseManifest('healthy-deployment', goodURL), readiness_path: '/not-ready' }, 0);
  assert.equal(retriedRelease.id, goodRelease.id);
  assert.equal(retriedRelease.run.id, goodRelease.run.id);
  assert.deepEqual(await api(`/api/projects/${healthy.project.id}/runs`), runsBeforeRetry);
  assert.deepEqual(await api(`/api/projects/${healthy.project.id}/repositories`), snapshotsBeforeRetry);
  const repeatedInput = { deployment_key: goodRelease.deployment_key, base_url: goodURL, mode: 'blocking', scenario_ids: [healthy.scenarios[0].id],
    repositories: goodRelease.repositories.map(({ snapshot_id, repository, role, commit_sha }) => ({ snapshot_id, repository, role, commit_sha })) };
  const retries = await Promise.all(Array.from({ length: 8 }, () => api(releaseEndpoint, repeatedInput)));
  assert.ok(retries.every(release => release.id === goodRelease.id && release.run.id === goodRelease.run.id));
  assert.deepEqual(await api(`/api/projects/${healthy.project.id}/runs`), runsBeforeRetry);
  await api(releaseEndpoint, { ...repeatedInput, base_url: badURL }, { status: 409 });
  await api(`/api/projects/${faulty.project.id}/releases/${goodRelease.id}`, undefined, { status: 404 });
  await stop(server);
  server = await start('api-release-restarted', resolve(evidence, 'server'), [], env);
  await ready(`${base}/healthz`, server);
  assert.deepEqual(await api(`${releaseEndpoint}/${goodRelease.id}`), goodRelease);
  observations.push({ name: 'Deployment releases pass healthy and block faulty targets, retain both revisions, and preserve project default', status: 'passed', boundary: sourceInputs ? 'seeded GitHub snapshots; real release API/database/browser/CLI resume' : 'live GitHub import and complete release CLI', release_ids: [goodRelease.id, badRelease.id] });
  observations.push({ name: 'Pipeline retries and concurrent requests reuse one run; changed inputs rejected; releases survive restart', status: 'passed' });
  // Push linked source records outside the recent-import window. Review links
  // must still retrieve the release's original snapshot by ID.
  const linkedBackend = badRelease.repositories.find(source => source.role === 'backend');
  command('docker', ['exec', container, 'psql', '-U', 'qa', '-d', 'qa_proof', '-v', 'ON_ERROR_STOP=1', '-c',
    `INSERT INTO repository_snapshots(id,project_id,repository,ref,role,commit_sha,content_sha256,files,file_count,total_bytes)
     SELECT md5(id || '-pagination-' || n),project_id,repository,ref,role,commit_sha,content_sha256,files,file_count,total_bytes
     FROM repository_snapshots CROSS JOIN generate_series(1,101) n WHERE id='${linkedBackend.snapshot_id}'`]);

  const webPort = await port();
  const webURL = `http://127.0.0.1:${webPort}`;
  const web = await start('web', process.execPath, ['web/node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', String(webPort), '--strictPort', 'web'], env);
  await ready(webURL, web);
  const { chromium } = await import('../worker/node_modules/playwright/index.mjs');
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    page.setDefaultTimeout(15000);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(webURL);
    await page.getByLabel('API token', { exact: true }).fill(apiToken);
    await page.getByRole('button', { name: 'Open workspace' }).click();
    await page.getByRole('button', { name: healthy.project.name, exact: true }).click();

    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('button', { name: 'Releases', exact: true }).click();
    const releaseDetails = page.getByRole('region', { name: 'Release details', exact: true });
    await releaseDetails.getByRole('heading', { name: 'faulty-deployment', exact: true }).waitFor();
    await releaseDetails.getByRole('link', { name: badURL, exact: true }).waitFor();
    assert.equal(await releaseDetails.getByText(sourceRevision, { exact: true }).count(), 2, 'Both full source revisions are visible');
    await releaseDetails.locator('details').first().locator('summary').click();
    await releaseDetails.getByText(badRelease.run.results[0].message, { exact: true }).waitFor();
    await page.screenshot({ path: resolve(evidence, 'release-desktop.png'), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'Mobile release details have no horizontal page overflow');
    await page.screenshot({ path: resolve(evidence, 'release-mobile.png'), fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await releaseDetails.locator('article').filter({ hasText: 'backend' }).getByRole('button', { name: 'Review saved snapshot', exact: false }).click();
    await page.getByText('sample/server.js', { exact: true }).first().waitFor();
    await page.getByRole('button', { name: 'Refresh', exact: true }).click();
    await page.getByText('sample/server.js', { exact: true }).first().waitFor();
    observations.push({ name: 'Desktop/mobile Releases show full revisions and faulty findings; linked snapshots older than 100 imports remain reviewable after refresh', status: 'passed' });

    if (repositorySnapshot) {
      await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('button', { name: 'Releases', exact: true }).click();
      await releaseDetails.getByRole('heading', { name: 'faulty-deployment', exact: true }).waitFor();
      await releaseDetails.locator('article').filter({ hasText: 'frontend' }).getByRole('button', { name: 'Review saved snapshot', exact: false }).click();
      await page.getByText('sample/scenarios.json', { exact: true }).waitFor();
      await page.screenshot({ path: resolve(evidence, 'repository-context.png'), fullPage: true });
    }

    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('button', { name: 'Discover', exact: true }).click();
    await page.getByLabel('Start path', { exact: false }).fill('/dashboard');
    await page.getByLabel('Approved setup scenario', { exact: false }).selectOption(healthy.scenarios[0].id);
    await page.getByRole('button', { name: 'Start discovery', exact: true }).click();
    await page.getByText('Discovery queued for /dashboard. Watch its status below.', { exact: true }).waitFor();
    for (let i = 0; i < 100; i++) {
      const jobs = await api(`/api/projects/${healthy.project.id}/discoveries`);
      if (jobs.length >= 3 && jobs[0].status === 'completed') break;
      if (i === 99) throw new Error('Web-created discovery did not complete');
      await new Promise(r => setTimeout(r, 200));
    }
    await page.getByRole('region', { name: 'Discovery history' }).getByRole('button', { name: 'Refresh', exact: true }).click();
    await page.getByRole('button', { name: 'Review observations', exact: true }).first().click();
    await page.getByText('dashboard-revenue', { exact: true }).waitFor();
    await page.screenshot({ path: resolve(evidence, 'browser-discovery.png'), fullPage: true });
    observations.push({ name: 'Web launches and reviews real browser discovery', status: 'passed' });
    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('button', { name: /^Scenarios/ }).click();
    await page.getByLabel('Select Missing identity blocks a run for next run', { exact: true }).uncheck();
    await page.getByLabel('Mode', { exact: true }).selectOption('blocking');
    await page.getByRole('button', { name: 'Run checks', exact: true }).click();
    await page.getByText('Every selected scenario completed and passed.', { exact: true }).waitFor({ timeout: 90000 });
    assert.deepEqual(errors, [], 'Web workspace must render without uncaught errors');
    if (await page.getByRole('button', { name: 'Dismiss notice', exact: true }).isVisible()) await page.getByRole('button', { name: 'Dismiss notice', exact: true }).click();
    await page.screenshot({ path: resolve(evidence, 'workspace-desktop.png'), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: resolve(evidence, 'workspace-mobile.png'), fullPage: true });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'Mobile layout has no horizontal page overflow');
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('button', { name: /^Scenarios/ }).click();
    await page.getByRole('button', { name: 'Ask AI to propose', exact: true }).click();
    await page.getByText('Proposal generation is unavailable', { exact: true }).waitFor();
    await page.getByRole('button', { name: 'Close AI proposals' }).click();
    // Only generation is simulated here. The review/save uses the real API/database.
    // Provider HTTP/schema/error behaviour is separately covered by Go tests.
    await page.route('**/api/ai/config', route => route.fulfill({ json: {
      provider: 'openai', models: ['test-model'], managed_available: false, byok_available: true,
    } }));
    const proposal = { ...templates[0], name: 'Proposed independent revenue check', approved: false };
    await page.route('**/proposals', async route => {
      assert.equal(route.request().headers()['x-qa-provider-key'], 'synthetic-provider-key');
      assert.equal(route.request().postDataJSON().consent, true);
      assert.ok(route.request().postDataJSON().discovery_id);
      await route.fulfill({ json: { provider: 'openai', model: 'test-model', context_sha256: 'synthetic-context', scenarios: [proposal], questions: ['Are multi-currency orders supported?'], assumptions: ['All fixture orders use one currency.'] } });
    });
    await page.getByRole('button', { name: 'Ask AI to propose', exact: true }).click();
    await page.getByRole('textbox', { name: /^Testing request/ }).fill('Check the independent net revenue calculation.');
    await page.getByRole('textbox', { name: /^Application context/ }).fill('Paid 150000 minus refund 10000 equals 140000; cancelled 90000 is excluded.');
    await page.getByRole('button', { name: 'Review observations', exact: true }).first().click();
    await page.getByRole('button', { name: 'Use these reviewed observations', exact: true }).click();
    await page.getByLabel('OpenAI API key', { exact: true }).fill('synthetic-provider-key');
    await page.getByRole('checkbox', { name: /I (agree to send|have reviewed)/ }).check();
    await page.getByRole('button', { name: 'Generate proposals', exact: true }).click();
    await page.getByText('Are multi-currency orders supported?', { exact: true }).waitFor();
    assert.equal(await page.getByLabel('OpenAI API key', { exact: true }).inputValue(), '');
    await page.getByRole('button', { name: 'Review in editor', exact: true }).click();
    const approvedBox = page.getByRole('checkbox', { name: 'Approve this scenario for runs', exact: true });
    assert.equal(await approvedBox.isChecked(), false, 'AI draft must not approve itself');
    await approvedBox.check();
    await page.getByRole('button', { name: 'Create scenario', exact: true }).click();
    await page.getByText('Confirm you reviewed the expected outcomes and assertions before approving.', { exact: true }).waitFor();
    await page.getByRole('checkbox', { name: /I reviewed the expected outcome/ }).check();
    await page.getByRole('button', { name: 'Create scenario', exact: true }).click();
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    const saved = await api(`/api/projects/${healthy.project.id}/scenarios`);
    assert.equal(saved.find(s => s.name === proposal.name)?.approved, true);
    observations.push({ name: 'AI draft review and approval UI (simulated generation, real persistence)', status: 'passed' });
    observations.push(await verifyChatGPTUI(page, api, healthy.project.id, templates[0], evidence));
    assert.deepEqual(errors, [], 'AI proposal flow must not throw browser errors');
    assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0, 'Operator token must not enter browser storage');
    await page.reload();
    await page.getByLabel('API token', { exact: true }).waitFor();
    observations.push({ name: 'Web workspace launches real checks and clears token on reload', status: 'passed' });
  } finally { await browser.close(); }
  console.log(JSON.stringify(observations, null, 2));
} finally {
  for (const child of children.reverse()) await stop(child);
  for (const file of files) await file.close();
  if (containerCreated) command('docker', ['rm', '-f', '-v', container]);
  manifest.cleanup = true;
  manifest.finished_at = new Date().toISOString();
  await writeFile(resolve(evidence, 'manifest.json'), JSON.stringify(manifest, null, 2));
  await writeFile(resolve(evidence, 'observations.json'), JSON.stringify(observations, null, 2));
  console.log(`Evidence: ${evidence}`);
}
