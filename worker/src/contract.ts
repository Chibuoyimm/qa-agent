import { createHash } from 'node:crypto';

export const locatorRoles = ['button', 'link', 'textbox', 'heading', 'checkbox', 'radio', 'combobox', 'option', 'tab', 'menuitem', 'alert', 'status', 'dialog', 'region', 'switch', 'spinbutton', 'searchbox'] as const;
export type ElementLocator = { role?: typeof locatorRoles[number]; name?: string; label?: string };

export type Step = {
  action: 'navigate' | 'fill' | 'click' | 'assert_text' | 'assert_visible' | 'assert_absent' | 'assert_path' | 'select_option' | 'phase' | 'use_actor' | 'prepare_fixture' | 'cleanup_fixture' | 'capture_id' | 'capture_json' | 'assert_json' | 'assert_json_number';
  save_as?: string;
  json_pointer?: string;
  value_ref?: string;
  path?: string;
  test_id?: string;
  locator?: ElementLocator;
  value?: string;
  secret_env?: string;
};
export type Scenario = { id: string; steps: Step[] };
export type Run = { id: string; base_url: string; scenarios: Scenario[] };
export type Artifact = { kind: 'screenshot' | 'video'; path: string; actor?: string };
export type ExecutionStepResult = {
  index: number; action: Step['action']; status: 'passed' | 'failed' | 'blocked' | 'error' | 'not_run';
  duration_ms: number; message?: string; observed?: string;
};
export type ScenarioResult = {
  step_results?: ExecutionStepResult[];
  scenario_id: string;
  status: 'passed' | 'failed' | 'blocked' | 'error' | 'running';
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

export function parseLocator(value: unknown): ElementLocator {
  if (!record(value) || !onlyFields(value, ['role', 'name', 'label'])) throw new Error('Invalid accessible locator');
  const validName = (name: unknown): name is string => typeof name === 'string' && name.trim().length > 0 && Buffer.byteLength(name) <= 300 && !/[\x00-\x08\x0a-\x1f]/.test(name);
  // Structured model output represents unused fields as null.
  if (value.label != null) {
    if (!validName(value.label) || value.role != null || value.name != null) throw new Error('Invalid label locator');
    return { label: value.label };
  }
  if (!locatorRoles.includes(value.role as typeof locatorRoles[number]) || !validName(value.name)) throw new Error('Invalid role locator');
  return { role: value.role as typeof locatorRoles[number], name: value.name };
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
  if (!record(candidate) || typeof candidate.id !== 'string' || !candidate.id || !Array.isArray(candidate.steps) || candidate.steps.length === 0 || candidate.steps.length > 200) {
    throw new Error('Invalid scenario snapshot');
  }
  const steps: Step[] = candidate.steps.map((step) => {
    if (!record(step) || !['navigate', 'fill', 'click', 'assert_text', 'assert_visible', 'assert_absent', 'assert_path', 'select_option', 'phase', 'use_actor', 'prepare_fixture', 'cleanup_fixture', 'capture_id', 'capture_json', 'assert_json', 'assert_json_number'].includes(String(step.action))) {
      throw new Error('Invalid step snapshot');
    }
    const action = step.action as Step['action'];
    if (['phase','use_actor','prepare_fixture','cleanup_fixture','capture_id','capture_json','assert_json','assert_json_number','assert_path'].includes(action)) return parseJourneyStep(step);
    if (action === 'navigate') {
      if (!onlyFields(step, ['action', 'path']) || typeof step.path !== 'string' || step.path.length > 2048 || !step.path.startsWith('/') || step.path.startsWith('//') || /[?#\\]/.test(step.path) || step.path.includes('..') || new URL(step.path, 'http://example.test').origin !== 'http://example.test') throw new Error('Invalid navigation path');
      return { action, path: step.path };
    }
    let target: Pick<Step, 'test_id' | 'locator'>;
    if (step.locator != null) {
      if (step.test_id != null && step.test_id !== '') throw new Error('Use exactly one target');
      target = { locator: parseLocator(step.locator) };
    } else {
      if (typeof step.test_id !== 'string' || !/^[\x21-\x7e]{1,200}$/.test(step.test_id) || /['"\\]/.test(step.test_id)) throw new Error('Invalid test ID');
      target = { test_id: step.test_id };
    }
    if (action === 'fill') {
      const literal = typeof step.value === 'string';
      const secret = typeof step.secret_env === 'string' && step.secret_env.length <= 100 && /^QA_TEST_[A-Z0-9_]+$/.test(step.secret_env);
      if (!onlyFields(step, ['action', 'test_id', 'locator', 'value', 'secret_env']) || literal === secret || (literal && 'secret_env' in step) || (secret && 'value' in step) || (literal && (step.value as string).length > 4000)) throw new Error('Invalid fill value');
      return { action, ...target, ...(literal ? { value: step.value as string } : { secret_env: step.secret_env as string }) };
    }
    if (action === 'assert_text' || action === 'select_option') {
      if (!onlyFields(step, ['action', 'test_id', 'locator', 'value']) || typeof step.value !== 'string' || step.value.length > 4000) throw new Error('Invalid expected text');
      return { action, ...target, value: step.value };
    }
    if (!onlyFields(step, ['action', 'test_id', 'locator'])) throw new Error('Invalid action fields');
    return { action, ...target };
  });
  validateBindings(steps);
  if (steps.length > 50 && !isJourney(steps)) throw new Error('Ordinary checks support up to fifty steps');
  if (!steps.some(step => step.action.startsWith('assert_'))) throw new Error('Scenario has no approved assertion');
  return { id: candidate.id, steps };
}

export const bindingName = /^[a-z][a-z0-9_]{0,39}$/;
export const bindingToken = /\{\{([a-z][a-z0-9_]{0,39})\}\}/g;
export const decimalLiteral = /^[+-]?[0-9]{1,100}(\.[0-9]{1,100})?$/;
export const isJourney = (steps: Step[]) => steps.some(s => ['phase','use_actor','prepare_fixture'].includes(s.action));
export const safeStepPath = (path: unknown): path is string => typeof path === 'string' && path.length > 0 && path.length <= 2048 && path.startsWith('/') && !path.startsWith('//') && !/[?#\\\s]/.test(path) && !path.includes('..');
const validPointer = (value: unknown): value is string => typeof value === 'string' && value.startsWith('/') && value.length <= 300 && !/[\x00-\x1f]/.test(value) && !/~(?![01])/.test(value);
const identifierPointer = (value: string) => /(?:^|\/)(?:id|[^/]+_id)$/.test(value);
function parseJourneyStep(step: Record<string, unknown>): Step {
 const action=step.action as Step['action'];
 if (['phase','use_actor','prepare_fixture','cleanup_fixture'].includes(action)) {
  if (!onlyFields(step,['action','value']) || typeof step.value!=='string' || !step.value.trim() || step.value.length>200 || (action!=='phase'&&!bindingName.test(step.value))) throw new Error('Invalid journey control');
  return {action,value:step.value};
 }
 if (action==='assert_path') {if (!onlyFields(step,['action','path']) || !safeStepPath(step.path)) throw new Error('Invalid asserted path');return {action,path:step.path};}
 if (action==='capture_id') {
  if (!onlyFields(step,['action','path','save_as']) || !safeStepPath(step.path) || !step.path.endsWith('/') || typeof step.save_as!=='string' || !bindingName.test(step.save_as)) throw new Error('Invalid record capture');
  return {action,path:step.path,save_as:step.save_as};
 }
 if (!onlyFields(step,['action','path','json_pointer','save_as','value','value_ref']) || !safeStepPath(step.path) || !validPointer(step.json_pointer)) throw new Error('Invalid JSON read');
 if (action==='capture_json') {
  if ('value' in step || 'value_ref' in step || typeof step.save_as!=='string' || !bindingName.test(step.save_as) || !identifierPointer(step.json_pointer)) throw new Error('Only identifier fields may be captured');
  return {action,path:step.path,json_pointer:step.json_pointer,save_as:step.save_as};
 }
 const literal=typeof step.value==='string',reference=typeof step.value_ref==='string';
 if ('save_as' in step || literal===reference || (literal&&('value_ref' in step || step.value!.toString().length>4000)) || (reference&&('value' in step || !bindingName.test(step.value_ref as string) || !identifierPointer(step.json_pointer))) || (action==='assert_json_number'&&(!literal||!decimalLiteral.test(step.value as string)))) throw new Error('Invalid fixed JSON expectation');
 return {action,path:step.path,json_pointer:step.json_pointer,...(literal?{value:step.value as string}:{value_ref:step.value_ref as string})};
}
function validateBindings(steps: Step[]) {
 const known=new Set(['run_id']),actors=new Set<string>();let fixture='';
 for (const [i,step] of steps.entries()) {
  for (const value of [step.path??'',step.action==='fill'?step.value??'':'']) {
   for (const match of value.matchAll(bindingToken)) if (!known.has(match[1])) throw new Error('Unknown journey binding');
   if (/[{}]{2}/.test(value.replace(bindingToken,''))) throw new Error('Malformed journey binding');
  }
  if (step.value_ref&&!known.has(step.value_ref)) throw new Error('Unknown identifier expectation');
  if (step.save_as) {if(known.has(step.save_as)) throw new Error('Bindings cannot be overwritten');known.add(step.save_as);}
  if (step.action==='use_actor') {actors.add(step.value!);if(actors.size>5) throw new Error('At most five actors');}
  if (step.action==='prepare_fixture') {if(i!==0) throw new Error('Fixture preparation must be first');fixture=step.value!;}
  if (step.action==='cleanup_fixture' && (i!==steps.length-1 || !fixture || step.value!==fixture)) throw new Error('Fixture cleanup must be last');
 }
 if(fixture&&steps.at(-1)?.action!=='cleanup_fixture') throw new Error('Fixture cleanup is required');
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
