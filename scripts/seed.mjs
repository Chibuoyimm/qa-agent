import { readFile } from 'node:fs/promises';

const base = process.env.QA_API_BASE_URL ?? 'http://127.0.0.1:8080';
const token = process.env.QA_API_TOKEN;
if (!token) throw new Error('Set QA_API_TOKEN before seeding.');
const target = process.env.QA_SAMPLE_URL ?? 'http://127.0.0.1:4174';
const api = async (path, body) => {
  const response = await fetch(`${base}${path}`, {
    method: 'POST', redirect: 'error',
    headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(body), signal: AbortSignal.timeout(15000),
  });
  if (!response.ok) throw new Error(`Seed request failed: HTTP ${response.status}`);
  return response.json();
};
const project = await api('/api/projects', { name: 'Sample SaaS — correctness proof', base_url: target });
const scenarios = JSON.parse(await readFile(new URL('../sample/scenarios.json', import.meta.url), 'utf8'));
const created = [];
for (const scenario of scenarios) created.push(await api(`/api/projects/${project.id}/scenarios`, scenario));
console.log(`Created project ${project.id} with ${created.length} approved scenarios.`);
console.log(`go run ./cmd/qa run --project ${project.id} --scenarios ${created.map(s => s.id).join(',')} --mode blocking`);
