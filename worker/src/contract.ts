import { createHash } from 'node:crypto';

export type Step = {
  action: 'navigate' | 'fill' | 'click' | 'assert_text' | 'assert_visible';
  path?: string;
  test_id?: string;
  value?: string;
  secret_env?: string;
};
export type Scenario = { id: string; steps: Step[] };
export type Run = { id: string; base_url: string; scenarios: Scenario[] };
export type Artifact = { kind: 'screenshot' | 'video'; path: string };
export type ScenarioResult = {
  scenario_id: string;
  status: 'passed' | 'failed' | 'blocked' | 'error';
  message: string;
  duration_ms: number;
  artifacts: Artifact[];
};
export type Claim = { run: Run; lease_token: string; lease_expires_at: string };

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function onlyFields(value: Record<string, unknown>, fields: string[]): boolean {
  return Object.keys(value).every(key => fields.includes(key));
}

export function parseClaim(value: unknown): Claim {
  if (!record(value) || !record(value.run) || typeof value.lease_token !== 'string' || !value.lease_token) {
    throw new Error('Invalid claim payload');
  }
  const run = value.run;
  if (typeof run.id !== 'string' || !run.id || typeof run.base_url !== 'string' || !Array.isArray(run.scenarios) || run.scenarios.length === 0 || run.scenarios.length > 50) {
    throw new Error('Invalid run snapshot');
  }
  const scenarios: Scenario[] = run.scenarios.map(parseScenario);
  if (new Set(scenarios.map(s => s.id)).size !== scenarios.length) throw new Error('Duplicate scenario ID');
  return { run: { id: run.id, base_url: run.base_url, scenarios }, lease_token: value.lease_token, lease_expires_at: String(value.lease_expires_at ?? '') };
}

export function parseScenario(candidate: unknown): Scenario {
  if (!record(candidate) || typeof candidate.id !== 'string' || !candidate.id || !Array.isArray(candidate.steps) || candidate.steps.length === 0 || candidate.steps.length > 50) {
    throw new Error('Invalid scenario snapshot');
  }
  const steps: Step[] = candidate.steps.map((step) => {
    if (!record(step) || !['navigate', 'fill', 'click', 'assert_text', 'assert_visible'].includes(String(step.action))) {
      throw new Error('Invalid step snapshot');
    }
    const action = step.action as Step['action'];
    if (action === 'navigate') {
      if (!onlyFields(step, ['action', 'path']) || typeof step.path !== 'string' || step.path.length > 2048 || !step.path.startsWith('/') || step.path.startsWith('//') || /[?#\\]/.test(step.path) || step.path.includes('..') || new URL(step.path, 'http://example.test').origin !== 'http://example.test') throw new Error('Invalid navigation path');
      return { action, path: step.path };
    }
    if (typeof step.test_id !== 'string' || !/^[\x21-\x7e]{1,200}$/.test(step.test_id) || /['"\\]/.test(step.test_id)) throw new Error('Invalid test ID');
    if (action === 'fill') {
      const literal = typeof step.value === 'string';
      const secret = typeof step.secret_env === 'string' && step.secret_env.length <= 100 && /^QA_TEST_[A-Z0-9_]+$/.test(step.secret_env);
      if (!onlyFields(step, ['action', 'test_id', 'value', 'secret_env']) || literal === secret || (literal && 'secret_env' in step) || (secret && 'value' in step) || (literal && (step.value as string).length > 4000)) throw new Error('Invalid fill value');
      return { action, test_id: step.test_id, ...(literal ? { value: step.value as string } : { secret_env: step.secret_env as string }) };
    }
    if (action === 'assert_text') {
      if (!onlyFields(step, ['action', 'test_id', 'value']) || typeof step.value !== 'string' || step.value.length > 4000) throw new Error('Invalid expected text');
      return { action, test_id: step.test_id, value: step.value };
    }
    if (!onlyFields(step, ['action', 'test_id'])) throw new Error('Invalid action fields');
    return { action, test_id: step.test_id };
  });
  if (!steps.some(step => step.action === 'assert_text' || step.action === 'assert_visible')) throw new Error('Scenario has no approved assertion');
  return { id: candidate.id, steps };
}

export function parseAllowedOrigins(value: string): Set<string> {
  const origins = value.split(',').map(s => s.trim()).filter(Boolean);
  if (!origins.length) throw new Error('QA_ALLOWED_ORIGINS is required');
  return new Set(origins.map(origin => {
    let url: URL;
    try { url = new URL(origin); } catch { throw new Error('QA_ALLOWED_ORIGINS must contain exact HTTP(S) origins'); }
    if (!['http:', 'https:'].includes(url.protocol) || url.origin !== origin || url.pathname !== '/' || url.username || url.password || url.search || url.hash) {
      throw new Error('QA_ALLOWED_ORIGINS must contain exact HTTP(S) origins');
    }
    return origin;
  }));
}

export function validateBaseUrl(value: string, allowedOrigins: Set<string>): URL {
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || !allowedOrigins.has(url.origin) || url.username || url.password || url.search || url.hash || url.pathname !== '/') {
    throw new Error('Run base URL is outside allowed origins');
  }
  return url;
}

export function safePart(value: string): string {
  // Opaque IDs never enter a filesystem path or a browser-visible error verbatim.
  return createHash('sha256').update(value).digest('hex').slice(0, 24);
}
