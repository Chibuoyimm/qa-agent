export type StepAction = 'navigate' | 'fill' | 'click' | 'assert_text' | 'assert_visible'

export type Step = {
  action: StepAction
  path?: string
  test_id?: string
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

export type Project = {
  id: string
  name: string
  base_url: string
  created_at: string
}

export type Scenario = ScenarioInput & {
  id: string
  project_id: string
  created_at: string
}

export type ResultStatus = 'passed' | 'failed' | 'blocked' | 'error'
export type RunStatus = 'queued' | 'running' | ResultStatus | 'cancelled'
export type RunMode = 'advisory' | 'blocking'
export type Gate = 'pending' | 'pass' | 'warn' | 'fail'

export type Artifact = { kind: 'screenshot' | 'video'; path: string }
export type ScenarioResult = {
  scenario_id: string
  status: ResultStatus
  message: string
  duration_ms: number
  artifacts: Artifact[]
}

export type Run = {
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
  } catch {
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
    case 'assert_visible': return { action, test_id: '' }
  }
}

export const emptyScenario = (): ScenarioInput => ({
  name: '', description: '', expected_outcome: '', approved: false,
  steps: [{ action: 'navigate', path: '/' }, { action: 'assert_visible', test_id: '' }],
})

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
  if (!Array.isArray(data.steps) || data.steps.length < 1 || data.steps.length > 50) throw new Error('Add 1–50 steps.')
  const steps = data.steps.map((raw, index): Step => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error(`Step ${index + 1} must be an object.`)
    const step = raw as Record<string, unknown>
    const action = step.action
    if (action !== 'navigate' && action !== 'fill' && action !== 'click' && action !== 'assert_text' && action !== 'assert_visible') throw new Error(`Step ${index + 1} has an unsupported action.`)
    const fields = action === 'navigate' ? ['action', 'path'] : action === 'fill' ? ['action', 'test_id', 'value', 'secret_env'] : action === 'assert_text' ? ['action', 'test_id', 'value'] : ['action', 'test_id']
    const extraField = Object.keys(step).find(key => !fields.includes(key))
    if (extraField) throw new Error(`Step ${index + 1}: remove “${extraField}” for ${action}.`)
    if (action === 'navigate') {
      if (typeof step.path !== 'string' || !step.path.startsWith('/') || step.path.startsWith('//')) throw new Error(`Step ${index + 1}: path must start with one “/”.`)
      return { action, path: step.path }
    }
    if (typeof step.test_id !== 'string' || !step.test_id.trim()) throw new Error(`Step ${index + 1}: test_id is required.`)
    if (action === 'fill') {
      const hasValue = typeof step.value === 'string'
      const hasSecret = typeof step.secret_env === 'string'
      if (hasValue === hasSecret) throw new Error(`Step ${index + 1}: fill needs exactly one of value or secret_env.`)
      if (hasSecret && !(step.secret_env as string).startsWith('QA_TEST_')) throw new Error(`Step ${index + 1}: secret_env must start with QA_TEST_.`)
      return hasSecret ? { action, test_id: step.test_id, secret_env: step.secret_env as string } : { action, test_id: step.test_id, value: step.value as string }
    }
    if (action === 'assert_text') {
      if (typeof step.value !== 'string') throw new Error(`Step ${index + 1}: exact expected text is required.`)
      return { action, test_id: step.test_id, value: step.value }
    }
    return { action, test_id: step.test_id }
  })
  if (data.approved && !steps.some(step => step.action === 'assert_text' || step.action === 'assert_visible')) throw new Error('Approved scenarios need at least one assertion.')
  return {
    name: data.name.trim(), description: data.description.trim(),
    expected_outcome: data.expected_outcome.trim(), approved: data.approved, steps,
  }
}
