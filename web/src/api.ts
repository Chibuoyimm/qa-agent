export type StepAction = 'navigate' | 'fill' | 'click' | 'assert_text' | 'assert_visible' | 'assert_absent' | 'assert_path' | 'select_option' | 'phase' | 'use_actor' | 'prepare_fixture' | 'cleanup_fixture' | 'capture_id' | 'capture_json' | 'assert_json' | 'assert_json_number'

export type ElementLocator = { role?: string; name?: string; label?: string }

export type Step = {
  action: StepAction
  save_as?: string
  json_pointer?: string
  value_ref?: string
  path?: string
  test_id?: string
  locator?: ElementLocator
  value?: string
  secret_env?: string
}

export type ScenarioInput = {
  name: string
  description: string
  expected_outcome: string
  approved: boolean
  steps: Step[]
}

export type FixtureProfile = { id: string; name: string; description: string; origins: string[]; secret_refs: string[] }

export type Project = {
  id: string
  name: string
  base_url: string
  created_at: string
}

export type Scenario = ScenarioInput & {
  journey_id?: string
  task_id?: string
  id: string
  project_id: string
  created_at: string
}

export type ResultStatus = 'passed' | 'failed' | 'blocked' | 'error'
export type RunStatus = 'queued' | 'running' | ResultStatus | 'cancelled'
export type RunMode = 'advisory' | 'blocking'
export type Gate = 'pending' | 'pass' | 'warn' | 'fail'

export type Artifact = { kind: 'screenshot' | 'video'; path: string; actor?: string }
export type ExecutionStepResult = { index: number; action: StepAction; status: ResultStatus | 'not_run'; duration_ms: number; message?: string; observed?: string }
export type ScenarioResult = {
  step_results?: ExecutionStepResult[]
  scenario_id: string
  status: ResultStatus | 'running'
  message: string
  duration_ms: number
  artifacts: Artifact[]
}

export type Run = {
  task_id?: string
  source_run_id?: string
  id: string
  project_id: string
  base_url: string
  mode: RunMode
  status: RunStatus
  gate: Gate
  scenarios: Scenario[]
  results: ScenarioResult[]
  created_at: string
  started_at?: string
  finished_at?: string
}

export type ReleaseRepository = {
  provider?: RepositoryProvider
  snapshot_id: string
  repository: string
  role: RepositoryRole
  commit_sha: string
  content_sha256: string
  paths: string[]
}

export type Release = {
  id: string
  project_id: string
  deployment_key: string
  base_url: string
  mode: RunMode
  repositories: ReleaseRepository[]
  run: Run
  created_at: string
}

export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
    this.name = 'ApiError'
  }
}

export async function api<T>(token: string, path: string, options: RequestInit = {}): Promise<T> {
  let response: Response
  try {
    response = await fetch(path, {
      ...options,
      headers: {
        Authorization: `Bearer ${token}`,
        ...(options.body ? { 'Content-Type': 'application/json' } : {}),
        ...options.headers,
      },
    })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new ApiError('Could not reach the API. Check that the server is running on 127.0.0.1:8080.', 0)
  }

  if (!response.ok) {
    const body = await response.json().catch(() => null) as { error?: unknown } | null
    const message = typeof body?.error === 'string' ? body.error : `Request failed (${response.status})`
    throw new ApiError(message, response.status)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const isActive = (status: RunStatus) => status === 'queued' || status === 'running'

export function formatDate(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return `${new Intl.DateTimeFormat('en-GB', {
    day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit',
    hour12: false, timeZone: 'Africa/Lagos',
  }).format(date)} WAT`
}

export function shortId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id
}

export const defaultStep = (action: StepAction): Step => {
  switch (action) {
    case 'navigate': return { action, path: '/' }
    case 'fill': return { action, test_id: '', value: '' }
    case 'click': return { action, test_id: '' }
    case 'assert_text': return { action, test_id: '', value: '' }
    case 'assert_visible': case 'assert_absent': return { action, test_id: '' }
    case 'phase': case 'use_actor': case 'prepare_fixture': case 'cleanup_fixture': return { action, value: '' }
    case 'assert_path': return { action, path: '/' }
    case 'capture_id': return { action, path: '/', save_as: '' }
    case 'capture_json': return { action, path: '/', save_as: '', json_pointer: '/id' }
    case 'assert_json': case 'assert_json_number': return { action, path: '/', json_pointer: '/status', value: '' }
    case 'select_option': return { action, test_id: '', value: '' }
  }
}

export const emptyScenario = (): ScenarioInput => ({
  name: '', description: '', expected_outcome: '', approved: false,
  steps: [{ action: 'navigate', path: '/' }, { action: 'assert_visible', test_id: '' }],
})

export type AiProvider = 'openai' | 'anthropic' | 'google' | 'opencode-go'
export const providerName = (provider: AiProvider): string => ({ openai: 'OpenAI', anthropic: 'Anthropic', google: 'Google Gemini', 'opencode-go': 'OpenCode Go' })[provider]

export type AiProviderConfig = {
  provider: AiProvider
  models: string[]
  managed_available: boolean
  byok_available: boolean
}

export type AiConfig = {
  providers?: AiProviderConfig[]
  provider: 'openai'
  models: string[]
  managed_available: boolean
  byok_available: boolean
  chatgpt?: ChatGptStatus
  opencode_enabled?: boolean
}

export type OpenCodeStatus = {
  enabled: boolean
  connected: boolean
  models: { id: string; name: string }[]
}

export type ChatGptProfile = {
  id: string
  email: string
  label: string
  connected: boolean
  sharing: boolean
}

export type ChatGptStatus = {
  enabled: boolean
  active_profile_id: string
  profiles: ChatGptProfile[]
  login?: { id: string; status: string; message: string }
  message?: string
}

export type ChatGptModel = { slug: string; display_name: string }
export type ChatGptLogin = { id: string; auth_url: string; expires_at: string }

export type AiProposalResponse = {
  provider: AiProvider
  model: string
  credential_mode?: string
  chatgpt_profile_id?: string
  context_sha256: string
  scenarios: ScenarioInput[]
  questions: string[]
  assumptions: string[]
  repository_snapshots?: RepositorySnapshotSummary[]
  discovery_id?: string
}

export type DiscoveryStatus = 'queued' | 'running' | 'completed' | 'error' | 'cancelled'
export type DiscoveryElement = {
  locator?: ElementLocator
  test_id: string
  tag: string
  role: string
  label: string
  text: string
  input_type: string
}
export type DiscoveryPage = {
  path: string
  title: string
  headings: string[]
  elements: DiscoveryElement[]
  links: { path: string; text: string }[]
  truncated: boolean
}
export type Discovery = {
  id: string
  project_id: string
  base_url: string
  start_path: string
  max_pages: number
  setup_scenario?: Scenario
  status: DiscoveryStatus
  result?: { pages: DiscoveryPage[]; limited: boolean; warnings: string[] }
  error: string
  created_at: string
  started_at?: string
  finished_at?: string
}

export type RepositoryProvider = 'github' | 'azure'
export type RepositoryRole = 'frontend' | 'backend'
export type RepositorySnapshotSummary = {
  provider?: RepositoryProvider
  id: string
  project_id: string
  repository: string
  ref: string
  role: RepositoryRole
  commit_sha: string
  content_sha256: string
  file_count: number
  total_bytes: number
  created_at: string
}
export type RepositorySnapshot = RepositorySnapshotSummary & {
  files: { path: string; content: string }[]
}

export const locatorRoles = ['button', 'link', 'textbox', 'heading', 'checkbox', 'radio', 'combobox', 'option', 'tab', 'menuitem', 'alert', 'status', 'dialog', 'region', 'switch', 'spinbutton', 'searchbox']
export function validateLocator(value: unknown): ElementLocator {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Choose a field label or role and exact name.')
  const locator = value as Record<string, unknown>
  const validName = (name: unknown): name is string => typeof name === 'string' && Boolean(name.trim()) && new TextEncoder().encode(name).length <= 300 && !/[\x00-\x08\x0a-\x1f]/.test(name)
  if (Object.keys(locator).some(key => !['role', 'name', 'label'].includes(key))) throw new Error('Unsupported accessible target field.')
  if (locator.label !== undefined) {
    if (!validName(locator.label) || locator.role !== undefined || locator.name !== undefined) throw new Error('A field label must be used on its own.')
    return { label: locator.label }
  }
  if (typeof locator.role !== 'string' || !locatorRoles.includes(locator.role) || !validName(locator.name)) throw new Error('Choose a supported role and exact accessible name.')
  return { role: locator.role, name: locator.name }
}

const bindingName = /^[a-z][a-z0-9_]{0,39}$/
const bindingToken = /\{\{([a-z][a-z0-9_]{0,39})\}\}/g
const safePath = (value: unknown): value is string => typeof value === 'string' && value.length <= 2048 && value.startsWith('/') && !value.startsWith('//') && !/[?#\\\s]/.test(value) && !value.includes('..')
function validateJourneyStep(step: Record<string, unknown>): Step {
 const action=step.action as StepAction
 const fields=['action','path','value','save_as','json_pointer','value_ref']
 if(Object.keys(step).some(k=>!fields.includes(k))) throw new Error('Remove unsupported journey fields.')
 if(['phase','use_actor','prepare_fixture','cleanup_fixture'].includes(action)) {
  if(Object.keys(step).some(k=>!['action','value'].includes(k)) || typeof step.value!=='string' || !step.value.trim() || step.value.length>200 || (action!=='phase'&&!bindingName.test(step.value))) throw new Error('Journey controls require a name in value.')
  return {action,value:step.value}
 }
 if(!safePath(step.path)) throw new Error('Use an approved same-origin path.')
 if(action==='assert_path') {if(Object.keys(step).some(k=>!['action','path'].includes(k))) throw new Error('Path assertions only accept path.');return {action,path:step.path}}
 if(action==='capture_id') {if(Object.keys(step).some(k=>!['action','path','save_as'].includes(k)) || !step.path.endsWith('/') || typeof step.save_as!=='string' || !bindingName.test(step.save_as))throw new Error('Record capture needs a path prefix and a saved name.');return {action,path:step.path,save_as:step.save_as}}
 if(typeof step.json_pointer!=='string'||!step.json_pointer.startsWith('/')||step.json_pointer.length>300||/~(?![01])/.test(step.json_pointer))throw new Error('A valid JSON pointer is required.')
 const identifier=/(?:^|\/)(?:id|[^/]+_id)$/.test(step.json_pointer)
 if(action==='capture_json') {if(step.value!==undefined||step.value_ref!==undefined||typeof step.save_as!=='string'||!bindingName.test(step.save_as)||!identifier)throw new Error('Capture an identifier field using save_as.');return {action,path:step.path,json_pointer:step.json_pointer,save_as:step.save_as}}
 const literal=typeof step.value==='string',reference=typeof step.value_ref==='string'
 if(step.save_as!==undefined||literal===reference||(reference&&(!identifier||!bindingName.test(step.value_ref as string)))||(literal&&(step.value as string).length>4000)||(action==='assert_json_number'&&(!literal||!/^[-+]?[0-9]{1,100}(\.[0-9]{1,100})?$/.test(step.value as string))))throw new Error('Use a fixed expected value, or a saved identifier for an ID field.')
 return {action,path:step.path,json_pointer:step.json_pointer,...(literal?{value:step.value as string}:{value_ref:step.value_ref as string})}
}
export function validateJourneyBindings(steps: Step[]) {
 const known=new Set(['run_id']),actors=new Set<string>();let fixture=''
 for(const [index,step] of steps.entries()) {
  for(const value of [step.path??'',step.action==='fill'?step.value??'':'']) {for(const match of value.matchAll(bindingToken)) if(!known.has(match[1])) throw new Error(`Step ${index+1} refers to an identifier before it is saved.`);if(/[{}]{2}/.test(value.replace(bindingToken,'')))throw new Error('Malformed journey reference.')}
  if(step.value_ref&&!known.has(step.value_ref))throw new Error('Expected identifier has not been saved.')
  if(step.save_as){if(known.has(step.save_as))throw new Error('Saved identifiers cannot be overwritten.');known.add(step.save_as)}
  if(step.action==='use_actor'){actors.add(step.value!);if(actors.size>5)throw new Error('Use up to five participants.')}
  if(step.action==='prepare_fixture'){if(index!==0)throw new Error('Prepare test data first.');fixture=step.value!}
  if(step.action==='cleanup_fixture'&&(index!==steps.length-1||!fixture||fixture!==step.value))throw new Error('Cleanup must close the matching test data profile last.')
 }
 if(fixture&&steps.at(-1)?.action!=='cleanup_fixture')throw new Error('Prepared test data needs an approved cleanup step.')
 if(steps.length>50&&!steps.some(s=>['phase','use_actor','prepare_fixture'].includes(s.action)))throw new Error('Ordinary checks support up to fifty steps.')
}

export function validateScenario(input: unknown): ScenarioInput {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('Each scenario must be a JSON object.')
  const data = input as Record<string, unknown>
  const allowed = ['name', 'description', 'expected_outcome', 'approved', 'steps']
  const extra = Object.keys(data).find(key => !allowed.includes(key))
  if (extra) throw new Error(`Remove unsupported scenario field “${extra}”.`)
  if (typeof data.name !== 'string' || !data.name.trim() || data.name.length > 200) throw new Error('Name is required and must be at most 200 characters.')
  if (typeof data.description !== 'string' || data.description.length > 4000) throw new Error('Description must be at most 4000 characters.')
  if (typeof data.expected_outcome !== 'string' || !data.expected_outcome.trim() || data.expected_outcome.length > 4000) throw new Error('Expected outcome is required and must be at most 4000 characters.')
  if (typeof data.approved !== 'boolean') throw new Error('Approved must be true or false.')
  if (!Array.isArray(data.steps) || data.steps.length < 1 || data.steps.length > 200) throw new Error('Add up to 50 check steps, or 200 journey steps.')
  const steps = data.steps.map((raw, index): Step => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error(`Step ${index + 1} must be an object.`)
    const step = raw as Record<string, unknown>
    const action = step.action as StepAction
    if (!['navigate','fill','click','assert_text','assert_visible','assert_absent','assert_path','select_option','phase','use_actor','prepare_fixture','cleanup_fixture','capture_id','capture_json','assert_json','assert_json_number'].includes(String(action))) throw new Error(`Step ${index + 1} has an unsupported action.`)
    if (['phase','use_actor','prepare_fixture','cleanup_fixture','capture_id','capture_json','assert_json','assert_json_number','assert_path'].includes(String(action))) return validateJourneyStep(step)
    const fields = action === 'navigate' ? ['action', 'path'] : action === 'fill' ? ['action', 'test_id', 'locator', 'value', 'secret_env'] : (action === 'assert_text' || action === 'select_option') ? ['action', 'test_id', 'locator', 'value'] : ['action', 'test_id', 'locator']
    const extraField = Object.keys(step).find(key => !fields.includes(key))
    if (extraField) throw new Error(`Step ${index + 1}: remove “${extraField}” for ${action}.`)
    if (action === 'navigate') {
      if (typeof step.path !== 'string' || !step.path.startsWith('/') || step.path.startsWith('//')) throw new Error(`Step ${index + 1}: path must start with one “/”.`)
      return { action, path: step.path }
    }
    let target: Pick<Step, 'test_id' | 'locator'>
    if (step.locator !== undefined) {
      if (step.test_id !== undefined) throw new Error(`Step ${index + 1}: choose one target.`)
      target = { locator: validateLocator(step.locator) }
    } else {
      if (typeof step.test_id !== 'string' || !/^[\x21-\x7e]{1,200}$/.test(step.test_id) || /['"\\]/.test(step.test_id)) throw new Error(`Step ${index + 1}: a valid test ID or accessible target is required.`)
      target = { test_id: step.test_id }
    }
    if (action === 'fill') {
      const hasValue = typeof step.value === 'string'
      const hasSecret = typeof step.secret_env === 'string'
      if (hasValue === hasSecret) throw new Error(`Step ${index + 1}: fill needs exactly one of value or secret_env.`)
      if (hasSecret && !(step.secret_env as string).startsWith('QA_TEST_')) throw new Error(`Step ${index + 1}: secret_env must start with QA_TEST_.`)
      return hasSecret ? { action, ...target, secret_env: step.secret_env as string } : { action, ...target, value: step.value as string }
    }
    if (action === 'assert_text' || action === 'select_option') {
      if (typeof step.value !== 'string') throw new Error(`Step ${index + 1}: exact expected text is required.`)
      return { action, ...target, value: step.value }
    }
    return { action, ...target }
  })
  validateJourneyBindings(steps)
  if (data.approved && !steps.some(step => step.action.startsWith('assert_'))) throw new Error('Approved scenarios need at least one assertion.')
  return {
    name: data.name.trim(), description: data.description.trim(),
    expected_outcome: data.expected_outcome.trim(), approved: data.approved, steps,
  }
}

export type ChatTurn = {
  task_id?: string
  id: string
  sequence: number
  project_id: string
  prompt: string
  status: 'pending' | 'completed' | 'error'
  reply: string
  proposal?: AiProposalResponse
  run_id?: string
  created_at: string
}
export type ChatPage = { turns: ChatTurn[]; has_older: boolean }
export type ChatRunAction = 'all_approved' | 'rerun_failed'

// Only explicit execution requests bypass drafting. Ambiguous requests stay with the model.
export function chatRunCommand(prompt: string): ChatRunAction | null {
  const text = prompt.trim().toLowerCase().replace(/[.!?]+$/, '').replace(/\s+/g, ' ')
  if (['run all approved checks', 'run all approved tests', 'run all checks', 'run a full check', 'run a full check of the app', 'full check'].includes(text)) return 'all_approved'
  if (['rerun failed checks', 'rerun the failed checks', 'rerun failures', 'rerun failed tests'].includes(text)) return 'rerun_failed'
  return null
}

export type Journey = { id: string; project_id: string; name: string; created_at: string }
export type TaskApproval = { turn_id: string; scenario_ids: string[] }
export type TestingTask = { id: string; project_id: string; journey_id: string; title: string; request: string; discovery_id?: string; created_at: string; approvals: TaskApproval[] }
export type CoverageCheck = { scenario: Scenario; result: ScenarioResult; run_id: string; base_url: string; finished_at: string }
export type CoverageData = { journeys: Journey[]; checks: CoverageCheck[]; scenarios: Scenario[] }
export type FindingReview = { scenario_id: string; status: string; owner: string; reason: string; updated_at?: string }
export const newRequestID = () => crypto.randomUUID().replaceAll('-', '')
