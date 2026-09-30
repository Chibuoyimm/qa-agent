import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import {
  Activity, AlertCircle, ArrowDownRight, ArrowRight, Check, CheckCircle2,
  ChevronDown, ChevronRight, CircleDashed, Clock3, Code2, Compass, FileJson2,
  FolderOpen, HelpCircle, KeyRound, Layers3, LoaderCircle, LockKeyhole,
  Menu, MoreHorizontal, Play, Plus, RefreshCw, Rocket, ShieldCheck, ShieldX,
  Sparkles, Square, Terminal, Trash2, X,
} from 'lucide-react'
import AiProposals from './AiProposals'
import RepositorySnapshots from './RepositorySnapshots'
import Discoveries from './Discoveries'
import Releases from './Releases'
import {
  api, defaultStep, emptyScenario, formatDate, isActive, shortId,
  validateScenario,
  type Project, type Run, type RunMode, type Scenario, type ScenarioInput,
  type Step, type StepAction,
} from './api'

type Page = 'overview' | 'scenarios' | 'repositories' | 'discoveries' | 'runs' | 'releases'
type EditorMode = 'guided' | 'json'

const statusText: Record<string, string> = {
  queued: 'Queued', running: 'Running', passed: 'Passed', failed: 'Failed',
  blocked: 'Blocked', error: 'Error', cancelled: 'Cancelled',
}
const gateText: Record<string, string> = {
  pending: 'Pending', pass: 'Pass', warn: 'Warning', fail: 'Fail',
}
const actionText: Record<StepAction, string> = {
  navigate: 'Navigate', fill: 'Fill field', click: 'Click',
  assert_text: 'Assert exact text', assert_visible: 'Assert visible',
}
const actionHint: Record<StepAction, string> = {
  navigate: 'Open a same-origin path, for example /dashboard.',
  fill: 'Enter literal text or a worker-local QA_TEST_ secret.',
  click: 'Click an element by its data-testid.',
  assert_text: 'Compare the element’s full text exactly.',
  assert_visible: 'Require an element with this test ID to be visible.',
}
const sampleScenario: ScenarioInput = {
  name: 'Net revenue excludes refunds and cancelled orders',
  description: 'Sign in to the controlled sample and verify its seeded revenue summary.',
  expected_outcome: 'Net revenue is 140000 from paid 150000 less refund 10000; the cancelled order is excluded.',
  approved: false,
  steps: [
    { action: 'navigate', path: '/login' },
    { action: 'fill', test_id: 'login-email', secret_env: 'QA_TEST_EMAIL' },
    { action: 'fill', test_id: 'login-password', secret_env: 'QA_TEST_PASSWORD' },
    { action: 'click', test_id: 'login-submit' },
    { action: 'assert_text', test_id: 'dashboard-revenue', value: '140000' },
    { action: 'assert_text', test_id: 'dashboard-orders', value: '3 orders' },
  ],
}

function ErrorBanner({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return <div className="error-banner" role="alert"><AlertCircle size={18} /><span>{message}</span>{onRetry && <button className="text-button" onClick={onRetry}>Retry</button>}</div>
}

function EmptyState({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return <div className="empty-state"><div className="empty-icon">{icon}</div><h3>{title}</h3><div className="empty-copy">{children}</div></div>
}

function StatusPill({ status }: { status: string }) {
  const icon = status === 'passed' || status === 'pass' ? <CheckCircle2 size={13} />
    : status === 'running' ? <LoaderCircle size={13} className="spin" />
      : status === 'queued' || status === 'pending' ? <Clock3 size={13} />
        : status === 'failed' || status === 'fail' ? <ShieldX size={13} />
          : status === 'blocked' ? <LockKeyhole size={13} />
            : status === 'error' ? <AlertCircle size={13} /> : <CircleDashed size={13} />
  return <span className={`pill pill-${status}`}>{icon}{statusText[status] ?? gateText[status] ?? status}</span>
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(1)} s`
}

function ScenarioEditor({ token, projectId, initial, source, onClose, onSaved }: { token: string; projectId: string; initial: ScenarioInput | null; source: 'new' | 'copy' | 'proposal'; onClose: () => void; onSaved: (scenario: Scenario) => void }) {
  const drawerRef = useRef<HTMLElement>(null)
  const [mode, setMode] = useState<EditorMode>('guided')
  const [draft, setDraft] = useState<ScenarioInput>(() => initial ? { ...initial, approved: false, steps: initial.steps.map(step => ({ ...step })) } : emptyScenario())
  const [json, setJson] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [reviewed, setReviewed] = useState(false)
  useEffect(() => {
    const drawer = drawerRef.current
    drawer?.querySelector<HTMLInputElement>('#scenario-name')?.focus()
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') { event.preventDefault(); onClose() }
      if (event.key !== 'Tab' || !drawer) return
      const controls = Array.from(drawer.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),textarea:not(:disabled),select:not(:disabled)'))
      if (controls.length === 0) return
      const first = controls[0], last = controls[controls.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  function updateDraft(patch: Partial<ScenarioInput>) {
    setDraft(previous => ({ ...previous, ...patch }))
    setError(''); setReviewed(false)
  }

  function updateStep(index: number, patch: Partial<Step>) {
    setDraft(previous => ({ ...previous, steps: previous.steps.map((step, i) => i === index ? { ...step, ...patch } : step) }))
    setError(''); setReviewed(false)
  }

  function changeAction(index: number, action: StepAction) {
    setDraft(previous => ({ ...previous, steps: previous.steps.map((step, i) => i === index ? defaultStep(action) : step) }))
    setReviewed(false)
  }

  function setEditorMode(next: EditorMode) {
    if (next === mode) return
    if (next === 'json') {
      setJson(JSON.stringify(draft, null, 2))
      setMode(next)
      setError('')
      return
    }
    try {
      const parsed = JSON.parse(json) as unknown
      if (Array.isArray(parsed)) throw new Error('Guided mode edits one scenario. Keep JSON mode to import multiple scenarios.')
      setDraft(validateScenario(parsed))
      setMode(next)
      setError(''); setReviewed(false)
    } catch (err) { setError(err instanceof Error ? err.message : 'Invalid JSON') }
  }

  async function save() {
    setError('')
    let saved = 0
    let inputs: ScenarioInput[] = []
    try {
      const raw: unknown = mode === 'json' ? JSON.parse(json) : draft
      inputs = (Array.isArray(raw) ? raw : [raw]).map(validateScenario)
      if (inputs.some(item => item.approved) && !reviewed) throw new Error('Confirm you reviewed the expected outcomes and assertions before approving.')
      if (!token || !projectId) throw new Error('Connection to the project was lost. Close and reopen this editor.')
      setSaving(true)
      for (const input of inputs) {
        const scenario = await api<Scenario>(token, `/api/projects/${encodeURIComponent(projectId)}/scenarios`, {
          method: 'POST', body: JSON.stringify(input),
        })
        saved += 1
        onSaved(scenario)
      }
      if (saved) onClose()
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Could not save scenarios'
      if (saved > 0) {
        const remaining = inputs.slice(saved)
        setJson(JSON.stringify(remaining.length === 1 ? remaining[0] : remaining, null, 2))
        setMode('json')
        setError(`${saved} ${saved === 1 ? 'scenario was' : 'scenarios were'} saved. ${remaining.length} remain in the editor. ${message}`)
      } else setError(message)
    } finally { setSaving(false) }
  }

  return <div className="drawer-backdrop" onMouseDown={event => { if (event.target === event.currentTarget) onClose() }}>
    <section className="drawer" ref={drawerRef} role="dialog" aria-modal="true" aria-labelledby="editor-title">
      <div className="drawer-header"><div><div className="eyebrow">SCENARIO DESIGNER</div><h2 id="editor-title">{source === 'copy' ? 'Review a copy' : source === 'proposal' ? 'Review AI proposal' : 'Add a scenario'}</h2><p>{source === 'copy' ? 'This creates a separate scenario. The existing check stays in coverage.' : source === 'proposal' ? 'Verify every proposed expectation and browser step before saving.' : 'Define the outcome, then add browser steps that prove it.'}</p></div><button className="icon-button" onClick={onClose} aria-label="Close editor"><X size={20} /></button></div>
      <div className="editor-tabs"><button className={mode === 'guided' ? 'active' : ''} onClick={() => setEditorMode('guided')}><Layers3 size={16} /> Guided editor</button><button className={mode === 'json' ? 'active' : ''} onClick={() => setEditorMode('json')}><Code2 size={16} /> JSON import</button></div>
      <div className="drawer-body">
        {mode === 'guided' ? <>
          <div className="form-section"><label className="field-label" htmlFor="scenario-name">Scenario name <span>*</span></label><input id="scenario-name" maxLength={200} placeholder="e.g. Net revenue excludes refunds and cancelled orders" value={draft.name} onChange={event => updateDraft({ name: event.target.value })} /></div>
          <div className="form-section"><label className="field-label" htmlFor="scenario-description">Description</label><textarea id="scenario-description" rows={2} maxLength={4000} placeholder="What user journey or business rule does this cover?" value={draft.description} onChange={event => updateDraft({ description: event.target.value })} /></div>
          <div className="form-section"><label className="field-label" htmlFor="scenario-expected">Expected outcome <span>*</span></label><textarea id="scenario-expected" rows={3} maxLength={4000} placeholder="State the business result independently of what the current UI displays." value={draft.expected_outcome} onChange={event => updateDraft({ expected_outcome: event.target.value })} /><p className="field-help">This is the approved rule. A run cannot change it to make a failure pass.</p></div>
          <div className="steps-heading"><div><div className="field-label">Browser steps <span>*</span></div><p className="field-help">Use paths and data-testid selectors. Add at least one assertion for approval.</p></div><span className="count-tag">{draft.steps.length} / 50</span></div>
          <div className="step-list">{draft.steps.map((step, index) => <div className="step-card" key={index}>
            <div className="step-top"><span className="step-number">{String(index + 1).padStart(2, '0')}</span><select aria-label={`Action for step ${index + 1}`} value={step.action} onChange={event => changeAction(index, event.target.value as StepAction)}>{Object.entries(actionText).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select><button className="icon-button small" aria-label={`Remove step ${index + 1}`} disabled={draft.steps.length === 1} onClick={() => updateDraft({ steps: draft.steps.filter((_, i) => i !== index) })}><Trash2 size={16} /></button></div>
            <p className="step-hint">{actionHint[step.action]}</p>
            {step.action === 'navigate' ? <input aria-label={`Path for step ${index + 1}`} placeholder="/dashboard" value={step.path ?? ''} onChange={event => updateStep(index, { path: event.target.value })} /> : <div className="step-fields"><input aria-label={`Test ID for step ${index + 1}`} placeholder="data-testid, e.g. dashboard-revenue" value={step.test_id ?? ''} onChange={event => updateStep(index, { test_id: event.target.value })} />
              {step.action === 'fill' && <><label className="inline-check"><input type="checkbox" checked={step.secret_env !== undefined} onChange={event => { const enabled = event.target.checked; setDraft(previous => ({ ...previous, steps: previous.steps.map((item, i) => i === index ? enabled ? { action: 'fill', test_id: item.test_id, secret_env: '' } : { action: 'fill', test_id: item.test_id, value: '' } : item) })); setReviewed(false) }} />Use worker-local secret</label><input aria-label={`${step.secret_env !== undefined ? 'Secret environment variable' : 'Value'} for step ${index + 1}`} placeholder={step.secret_env !== undefined ? 'QA_TEST_PASSWORD' : 'Text to enter'} value={step.secret_env !== undefined ? step.secret_env : step.value ?? ''} onChange={event => updateStep(index, step.secret_env !== undefined ? { secret_env: event.target.value } : { value: event.target.value })} /></>}
              {step.action === 'assert_text' && <input aria-label={`Exact text for step ${index + 1}`} placeholder="Exact text expected on screen" value={step.value ?? ''} onChange={event => updateStep(index, { value: event.target.value })} />}
            </div>}</div>)}</div>
          <button className="button button-soft add-step" disabled={draft.steps.length >= 50} onClick={() => updateDraft({ steps: [...draft.steps, defaultStep('assert_visible')] })}><Plus size={16} /> Add step</button>
          <div className="approval-box"><label className="inline-check strong"><input type="checkbox" checked={draft.approved} onChange={event => updateDraft({ approved: event.target.checked })} />Approve this scenario for runs</label><p>Unapproved scenarios stay in coverage but cannot be selected for a run.</p></div>
        </> : <>
          <div className="json-intro"><FileJson2 size={20} /><div><strong>Import one scenario or an array</strong><p>Paste JSON using only the fields in the contract. Each valid item is saved as a separate scenario.</p></div></div>
          <button className="sample-template" onClick={() => { setJson(JSON.stringify(sampleScenario, null, 2)); setReviewed(false); setError('') }}>Load controlled sample revenue example <ArrowRight size={14} /></button>
          <textarea className="json-editor" aria-label="Scenario JSON" spellCheck={false} value={json} onChange={event => { setJson(event.target.value); setError(''); setReviewed(false) }} />
          <div className="schema-guide"><strong>Allowed actions</strong><div className="schema-grid"><span><code>navigate</code> → <code>path</code></span><span><code>fill</code> → <code>test_id</code> + <code>value</code> or <code>secret_env</code></span><span><code>click</code> → <code>test_id</code></span><span><code>assert_text</code> → <code>test_id</code> + exact <code>value</code></span><span><code>assert_visible</code> → <code>test_id</code></span></div><p>Secrets use worker-local names beginning with <code>QA_TEST_</code>. JavaScript and arbitrary selectors are not accepted.</p></div>
        </>}
        <label className="review-check"><input type="checkbox" checked={reviewed} onChange={event => setReviewed(event.target.checked)} /><span>I reviewed the expected outcome and assertion steps for any scenario marked approved.</span></label>
        {error && <ErrorBanner message={error} />}
      </div>
      <div className="drawer-footer"><button className="button button-ghost" onClick={onClose}>Cancel</button><button className="button button-primary" disabled={saving} onClick={save}>{saving ? <LoaderCircle size={16} className="spin" /> : <Plus size={16} />}{saving ? 'Saving…' : source === 'copy' ? 'Save reviewed copy' : 'Create scenario'}</button></div>
    </section>
  </div>
}

function RunDetail({ run, onCancel, cancelling }: { run: Run; onCancel: () => void; cancelling: boolean }) {
  const resultMap = new Map(run.results.map(result => [result.scenario_id, result]))
  const counts = { passed: 0, failed: 0, blocked: 0, error: 0 }
  run.results.forEach(result => { counts[result.status] += 1 })
  const incomplete = run.scenarios.length - run.results.length
  return <div className="run-detail">
    <div className="detail-head"><div><div className="eyebrow">RUN DETAIL · {shortId(run.id)}</div><h2>Execution report</h2><div className="detail-meta"><StatusPill status={run.status} /><StatusPill status={run.gate} /><span>{run.mode === 'blocking' ? 'Blocking gate' : 'Advisory mode'}</span></div></div>{isActive(run.status) && <button className="button button-danger" disabled={cancelling} onClick={onCancel}><Square size={14} />{cancelling ? 'Cancelling…' : 'Cancel run'}</button>}</div>
    <div className="report-note">{run.status === 'queued' ? 'Waiting for a browser worker to claim this run.' : run.status === 'running' ? 'Browser checks are in progress. Results update automatically.' : run.status === 'cancelled' ? 'This run was cancelled; unfinished scenarios were not checked.' : run.status === 'passed' ? 'Every selected scenario completed and passed.' : run.status === 'failed' ? 'At least one assertion did not match its approved outcome.' : run.status === 'blocked' ? 'A required prerequisite was missing, such as a worker-local secret.' : 'An execution or infrastructure error prevented a complete proof.'}</div>
    <div className="report-grid"><div><span>Selected</span><strong>{run.scenarios.length}</strong></div><div><span>Passed</span><strong className="tone-pass">{counts.passed}</strong></div><div><span>Failed</span><strong className="tone-fail">{counts.failed}</strong></div><div><span>Blocked</span><strong className="tone-block">{counts.blocked}</strong></div><div><span>Error</span><strong className="tone-error">{counts.error}</strong></div><div><span>Incomplete</span><strong>{incomplete}</strong></div></div>
    <div className="run-facts"><span>Started: {formatDate(run.started_at)}</span><span>Finished: {formatDate(run.finished_at)}</span><span>Target: <code>{run.base_url}</code></span></div>
    {incomplete > 0 && <div className="incomplete-note"><CircleDashed size={17} /><span>{incomplete} of {run.scenarios.length} selected {incomplete === 1 ? 'scenario has' : 'scenarios have'} no result. This run does not prove complete coverage.</span></div>}
    <div className="result-list">{run.scenarios.map((scenario, index) => {
      const result = resultMap.get(scenario.id)
      return <details className="result-card" key={scenario.id} open={result?.status !== 'passed'}><summary><span className="result-index">{String(index + 1).padStart(2, '0')}</span><span className="result-title"><strong>{scenario.name}</strong><small>{scenario.expected_outcome}</small></span>{result ? <StatusPill status={result.status} /> : <span className="pill pill-pending"><Clock3 size={13} />Not completed</span>}<ChevronDown size={16} className="summary-chevron" /></summary><div className="result-body">{result ? <><p className="result-message">{result.message || (result.status === 'passed' ? 'Assertion passed.' : 'No finding was provided.')}</p><div className="result-meta">Duration {formatDuration(result.duration_ms)}</div>{result.artifacts.length > 0 && <div className="artifacts"><div className="field-label">Local worker files</div>{result.artifacts.map((artifact, i) => <div className="artifact" key={i}><FolderOpen size={15} /><span>{artifact.kind}</span><code>{artifact.path}</code></div>)}<p>Paths are relative to the worker’s artifact directory. They are metadata, not hosted links.</p></div>}</> : <p className="muted">No result was recorded for this scenario.</p>}</div></details>
    })}</div>
  </div>
}

function App() {
  const loadSequence = useRef(0)
  const selectionProject = useRef('')
  const activeProject = useRef('')
  const activeToken = useRef('')
  const activeRun = useRef('')
  const [tokenInput, setTokenInput] = useState('')
  const [token, setToken] = useState('')
  const [projects, setProjects] = useState<Project[]>([])
  const [projectsLoading, setProjectsLoading] = useState(false)
  const [projectsError, setProjectsError] = useState('')
  const [connectionState, setConnectionState] = useState<'connecting' | 'connected' | 'error'>('connecting')
  const [projectId, setProjectId] = useState('')
  const [showProjectForm, setShowProjectForm] = useState(false)
  const [page, setPage] = useState<Page>('overview')
  const [scenarios, setScenarios] = useState<Scenario[]>([])
  const [runs, setRuns] = useState<Run[]>([])
  const [dataLoading, setDataLoading] = useState(false)
  const [dataError, setDataError] = useState('')
  const [selectedRunId, setSelectedRunId] = useState('')
  const [selectedRun, setSelectedRun] = useState<Run | null>(null)
  const [selectedScenarioIds, setSelectedScenarioIds] = useState<string[]>([])
  const [mode, setMode] = useState<RunMode>('advisory')
  const [editorOpen, setEditorOpen] = useState(false)
  const [editorDraft, setEditorDraft] = useState<ScenarioInput | null>(null)
  const [editorSource, setEditorSource] = useState<'new' | 'copy' | 'proposal'>('new')
  const [aiOpen, setAiOpen] = useState(false)
  const [creatingProject, setCreatingProject] = useState(false)
  const [projectName, setProjectName] = useState('')
  const [projectUrl, setProjectUrl] = useState('')
  const [projectFormError, setProjectFormError] = useState('')
  const [launching, setLaunching] = useState(false)
  const [cancelling, setCancelling] = useState(false)
  const [notice, setNotice] = useState('')
  const [mobileNav, setMobileNav] = useState(false)
  const [focusSnapshotId, setFocusSnapshotId] = useState('')

  const project = projects.find(item => item.id === projectId)
  const approved = scenarios.filter(item => item.approved)
  const latestRun = runs[0]
  activeProject.current = projectId
  activeToken.current = token
  activeRun.current = selectedRunId

  const loadProjects = useCallback(async (authToken: string) => {
    setProjectsLoading(true); setProjectsError(''); setConnectionState('connecting')
    try {
      const items = await api<Project[]>(authToken, '/api/projects')
      if (activeToken.current !== authToken) return
      setProjects(items)
      setConnectionState('connected')
      setProjectId(current => items.some(item => item.id === current) ? current : items[0]?.id ?? '')
    } catch (err) {
      if (activeToken.current === authToken) { setProjectsError(err instanceof Error ? err.message : 'Could not load projects'); setConnectionState('error') }
    } finally { if (activeToken.current === authToken) setProjectsLoading(false) }
  }, [])

  const loadProjectData = useCallback(async (authToken: string, id: string) => {
    const sequence = ++loadSequence.current
    setDataLoading(true); setDataError('')
    try {
      const [scenarioItems, runItems] = await Promise.all([
        api<Scenario[]>(authToken, `/api/projects/${encodeURIComponent(id)}/scenarios`),
        api<Run[]>(authToken, `/api/projects/${encodeURIComponent(id)}/runs`),
      ])
      if (sequence !== loadSequence.current || activeProject.current !== id || activeToken.current !== authToken) return
      setScenarios(scenarioItems); setRuns(runItems)
      const firstLoadForProject = selectionProject.current !== id
      selectionProject.current = id
      setSelectedScenarioIds(current => {
        const valid = current.filter(selected => scenarioItems.some(item => item.id === selected && item.approved))
        return firstLoadForProject ? scenarioItems.filter(item => item.approved).map(item => item.id) : valid
      })
      setSelectedRunId(current => runItems.some(item => item.id === current) ? current : runItems[0]?.id ?? '')
      if (runItems.length === 0) setSelectedRun(null)
    } catch (err) { if (sequence === loadSequence.current && activeProject.current === id && activeToken.current === authToken) setDataError(err instanceof Error ? err.message : 'Could not load project data') }
    finally { if (sequence === loadSequence.current && activeProject.current === id && activeToken.current === authToken) setDataLoading(false) }
  }, [])

  useEffect(() => { if (token) void loadProjects(token) }, [token, loadProjects])
  useEffect(() => { setFocusSnapshotId(''); if (token && projectId) { setScenarios([]); setRuns([]); setSelectedRun(null); setSelectedRunId(''); setSelectedScenarioIds([]); void loadProjectData(token, projectId) } }, [token, projectId, loadProjectData])
  useEffect(() => {
    if (!token || !selectedRunId) return
    let cancelled = false
    void api<Run>(token, `/api/runs/${encodeURIComponent(selectedRunId)}`).then(run => { if (!cancelled && activeRun.current === run.id && activeProject.current === run.project_id) setSelectedRun(run) }).catch(err => { if (!cancelled && activeRun.current === selectedRunId) setDataError(err instanceof Error ? err.message : 'Could not load run') })
    return () => { cancelled = true }
  }, [token, selectedRunId])
  useEffect(() => {
    if (!token || !projectId || !selectedRun || !isActive(selectedRun.status)) return
    let cancelled = false
    const timer = window.setInterval(() => {
      void api<Run>(token, `/api/runs/${encodeURIComponent(selectedRun.id)}`).then(run => {
        if (cancelled || activeRun.current !== run.id || activeProject.current !== run.project_id) return
        setSelectedRun(run)
        setRuns(previous => previous.map(item => item.id === run.id ? run : item))
      }).catch(err => { if (!cancelled && activeRun.current === selectedRun.id) setDataError(err instanceof Error ? err.message : 'Could not refresh run') })
    }, 3000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [token, projectId, selectedRun?.id, selectedRun?.status])
  useEffect(() => { if (notice) { const timer = window.setTimeout(() => setNotice(''), 5000); return () => window.clearTimeout(timer) } }, [notice])

  const coverage = useMemo(() => ({
    approved: scenarios.filter(item => item.approved).length,
    unapproved: scenarios.filter(item => !item.approved).length,
  }), [scenarios])

  async function createProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setProjectFormError('')
    if (!projectName.trim() || !projectUrl.trim()) { setProjectFormError('Enter a project name and target URL.'); return }
    const authToken = token
    setCreatingProject(true)
    try {
      const created = await api<Project>(authToken, '/api/projects', { method: 'POST', body: JSON.stringify({ name: projectName.trim(), base_url: projectUrl.trim() }) })
      if (activeToken.current !== authToken) return
      setProjects(previous => [...previous, created]); setProjectId(created.id); setShowProjectForm(false); setProjectName(''); setProjectUrl(''); setNotice('Project created.')
    } catch (err) { if (activeToken.current === authToken) setProjectFormError(err instanceof Error ? err.message : 'Could not create project') }
    finally { setCreatingProject(false) }
  }

  async function launchRun() {
    if (!projectId || selectedScenarioIds.length === 0) return
    const id = projectId, authToken = token
    setLaunching(true); setDataError('')
    try {
      const run = await api<Run>(authToken, `/api/projects/${encodeURIComponent(id)}/runs`, { method: 'POST', body: JSON.stringify({ scenario_ids: selectedScenarioIds, mode }) })
      if (activeProject.current !== id || activeToken.current !== authToken || run.project_id !== id) return
      setRuns(previous => [run, ...previous]); setSelectedRunId(run.id); setSelectedRun(run); setPage('runs'); setNotice('Run queued. Waiting for a browser worker.')
    } catch (err) { if (activeProject.current === id && activeToken.current === authToken) setDataError(err instanceof Error ? err.message : 'Could not start run') }
    finally { setLaunching(false) }
  }

  async function cancelRun() {
    if (!selectedRun) return
    const runId = selectedRun.id, id = projectId, authToken = token
    setCancelling(true); setDataError('')
    try {
      const run = await api<Run>(authToken, `/api/runs/${encodeURIComponent(runId)}/cancel`, { method: 'POST' })
      if (activeProject.current !== id || activeToken.current !== authToken || activeRun.current !== runId || run.project_id !== id) return
      setSelectedRun(run); setRuns(previous => previous.map(item => item.id === run.id ? run : item)); setNotice('Run cancelled.')
    } catch (err) { if (activeProject.current === id && activeToken.current === authToken && activeRun.current === runId) setDataError(err instanceof Error ? err.message : 'Could not cancel run') }
    finally { setCancelling(false) }
  }

  function openEditor(initial: ScenarioInput | null = null, source: 'new' | 'copy' | 'proposal' = 'new') { setEditorDraft(initial); setEditorSource(source); setEditorOpen(true) }
  const closeEditor = useCallback(() => setEditorOpen(false), [])
  function reviewCopy(scenario: Scenario) {
    openEditor({ name: scenario.name, description: scenario.description, expected_outcome: scenario.expected_outcome, approved: false, steps: scenario.steps.map(step => ({ ...step })) }, 'copy')
  }
  function savedScenario(scenario: Scenario) {
    if (scenario.project_id !== activeProject.current) return
    setScenarios(previous => [...previous, scenario])
    if (scenario.approved) setSelectedScenarioIds(previous => [...previous, scenario.id])
    setNotice('Scenario created.')
  }
  function toggleScenario(id: string) { setSelectedScenarioIds(previous => previous.includes(id) ? previous.filter(item => item !== id) : [...previous, id]) }
  function selectPage(next: Page) { setPage(next); if (next !== 'scenarios') setAiOpen(false); setShowProjectForm(false); setMobileNav(false) }
  function openProjectForm() { setPage('overview'); setShowProjectForm(true); setMobileNav(false) }
  function disconnect() { ++loadSequence.current; selectionProject.current = ''; setToken(''); setTokenInput(''); setProjects([]); setProjectId(''); setScenarios([]); setRuns([]); setSelectedRun(null); setSelectedRunId(''); setFocusSnapshotId(''); setConnectionState('connecting') }

  if (!token) return <div className="auth-page"><div className="auth-grid" /><div className="auth-content"><div className="brand brand-auth"><span className="brand-mark"><Activity size={24} strokeWidth={2.5} /></span><span>qa<span className="brand-accent">agent</span><small>WORKSPACE</small></span></div><div className="auth-card"><div className="auth-symbol"><KeyRound size={24} /></div><div className="eyebrow">LOCAL PILOT · MILESTONE 01</div><h1>Proof you can repeat.</h1><p>Review approved checks, run them in a real browser, and see exactly where the evidence stands.</p><form onSubmit={event => { event.preventDefault(); if (tokenInput.trim()) setToken(tokenInput.trim()) }}><label className="field-label" htmlFor="api-token">API token</label><div className="token-input"><LockKeyhole size={18} /><input id="api-token" type="password" autoComplete="off" placeholder="Enter QA_API_TOKEN" value={tokenInput} onChange={event => setTokenInput(event.target.value)} /></div><button className="button button-primary auth-submit" disabled={!tokenInput.trim()}>Open workspace <ArrowRight size={17} /></button></form><div className="auth-foot"><ShieldCheck size={16} /><span>Your token stays in this browser tab’s memory and clears on reload.</span></div></div><p className="auth-caption">Designed for a trusted, controlled test environment.</p></div></div>

  return <div className="app-shell">
    <aside className={`sidebar ${mobileNav ? 'sidebar-open' : ''}`}><div className="sidebar-top"><div className="brand"><span className="brand-mark"><Activity size={20} strokeWidth={2.5} /></span><span>qa<span className="brand-accent">agent</span><small>WORKSPACE</small></span></div><button className="icon-button mobile-close" onClick={() => setMobileNav(false)} aria-label="Close navigation"><X size={20} /></button></div>
      <div className="sidebar-section-label">WORKSPACE</div><div className="project-switcher"><div className="project-avatar">{project?.name?.charAt(0).toUpperCase() ?? 'P'}</div><div><strong>{project?.name ?? 'No project'}</strong><small>{project ? new URL(project.base_url).host : 'Create your first project'}</small></div><ChevronDown size={15} /></div>
      <div className="sidebar-section-label nav-label">NAVIGATION</div><nav aria-label="Main navigation"><button className={page === 'overview' ? 'nav-item active' : 'nav-item'} onClick={() => selectPage('overview')}><Layers3 size={18} />Overview</button><button className={page === 'scenarios' ? 'nav-item active' : 'nav-item'} onClick={() => selectPage('scenarios')}><ShieldCheck size={18} />Scenarios{scenarios.length > 0 && <span className="nav-count">{scenarios.length}</span>}</button><button className={page === 'repositories' ? 'nav-item active' : 'nav-item'} onClick={() => selectPage('repositories')}><Code2 size={18} />Repository</button><button className={page === 'discoveries' ? 'nav-item active' : 'nav-item'} onClick={() => selectPage('discoveries')}><Compass size={18} />Discover</button><button className={page === 'runs' ? 'nav-item active' : 'nav-item'} onClick={() => selectPage('runs')}><Play size={18} />Runs{runs.length > 0 && <span className="nav-count">{runs.length}</span>}</button><button className={page === 'releases' ? 'nav-item active' : 'nav-item'} onClick={() => selectPage('releases')}><Rocket size={18} />Releases</button></nav>
      <div className="sidebar-projects"><div className="sidebar-section-label">PROJECTS <button className="icon-button small" aria-label="Create project" onClick={openProjectForm}><Plus size={15} /></button></div>{projects.map(item => <button key={item.id} className={`project-link ${item.id === projectId ? 'selected' : ''}`} onClick={() => { setFocusSnapshotId(''); setProjectId(item.id); selectPage('overview') }}><span className="project-dot" />{item.name}</button>)}</div>
      <div className="sidebar-bottom"><div className="connection"><span className={`connection-dot connection-${connectionState}`} /><span>{connectionState === 'connecting' ? 'Connecting to API' : connectionState === 'connected' ? 'API connected' : 'API unavailable'}</span></div><button className="sidebar-signout" onClick={disconnect}><KeyRound size={16} />Disconnect token</button></div>
    </aside>
    <main className="main"><header className="topbar"><button className="icon-button mobile-menu" onClick={() => setMobileNav(true)} aria-label="Open navigation"><Menu size={21} /></button><div className="breadcrumb">Workspace <ChevronRight size={15} /> <strong>{page === 'overview' ? 'Overview' : page === 'scenarios' ? 'Scenarios' : page === 'repositories' ? 'Repository' : page === 'discoveries' ? 'Discover' : page === 'releases' ? 'Releases' : 'Runs'}</strong></div><div className="topbar-right"><span className="environment"><span />LOCAL ENVIRONMENT</span><span className="topbar-avatar">OP</span></div></header>
      <div className="content">
        {notice && <div className="toast"><Check size={16} />{notice}<button aria-label="Dismiss notice" onClick={() => setNotice('')}><X size={14} /></button></div>}
        {projectsError && <ErrorBanner message={projectsError} onRetry={() => void loadProjects(token)} />}
        {projectsLoading && projects.length === 0 ? <div className="loading-state"><LoaderCircle className="spin" size={24} />Loading projects…</div> : <>
          {(!project || showProjectForm) && <section className="setup-layout"><div className="setup-copy"><div className="eyebrow">{project ? 'NEW TARGET' : 'GET STARTED'}</div><h1>Set up a project</h1><p>Connect one controlled app origin to start defining checks. The API validates the target against its configured origin allowlist.</p><div className="setup-aside"><ShieldCheck size={20} /><div><strong>Local pilot boundary</strong><p>Use an origin you control and have listed in <code>QA_ALLOWED_ORIGINS</code>. Browser runs use the same boundary.</p></div></div></div><form className="setup-form panel" onSubmit={createProject}><div className="panel-kicker">NEW PROJECT</div><h2>Project details</h2><label className="field-label" htmlFor="project-name">Project name</label><input id="project-name" placeholder="e.g. Revenue dashboard" value={projectName} maxLength={200} onChange={event => setProjectName(event.target.value)} /><label className="field-label" htmlFor="project-url">Target base URL</label><input id="project-url" type="url" placeholder="http://127.0.0.1:4174" value={projectUrl} onChange={event => setProjectUrl(event.target.value)} /><p className="field-help">Origin only: no path, query, or fragment.</p>{projectFormError && <ErrorBanner message={projectFormError} />}<button className="button button-primary" disabled={creatingProject}>{creatingProject ? <LoaderCircle size={16} className="spin" /> : <Plus size={16} />}{creatingProject ? 'Creating…' : 'Create project'}</button>{project && <button className="button button-ghost" type="button" onClick={() => setShowProjectForm(false)}>Cancel</button>}</form></section>}
          {project && !showProjectForm && <>
            {dataError && <ErrorBanner message={dataError} onRetry={() => void loadProjectData(token, projectId)} />}
            {dataLoading ? <div className="loading-state"><LoaderCircle className="spin" size={24} />Loading workspace…</div> : <>
              {page === 'overview' && <><div className="page-heading"><div><div className="eyebrow">PROJECT OVERVIEW</div><h1>{project.name}</h1><p>One place to track configured coverage and the latest browser proof.</p></div><button className="button button-outline" onClick={() => void loadProjectData(token, projectId)}><RefreshCw size={16} />Refresh</button></div>
                <div className="target-strip"><span className="target-icon"><Terminal size={18} /></span><div><small>TARGET ORIGIN</small><strong>{project.base_url}</strong></div><span className="target-type">CONTROLLED APP</span></div>
                <div className="metric-grid"><div className="metric-card"><span>Configured scenarios</span><strong>{scenarios.length}</strong><small>{scenarios.length ? 'Defined checks in this project' : 'Start with your first check'}</small><Layers3 size={20} /></div><div className="metric-card"><span>Approved for runs</span><strong>{coverage.approved}</strong><small>Reviewed outcome and assertions</small><ShieldCheck size={20} /></div><div className="metric-card"><span>Awaiting approval</span><strong>{coverage.unapproved}</strong><small>Cannot be included in a run</small><Clock3 size={20} /></div><div className="metric-card"><span>Latest run</span><strong className="metric-status">{latestRun ? statusText[latestRun.status] : 'None yet'}</strong><small>{latestRun ? formatDate(latestRun.created_at) : 'No browser execution yet'}</small><Activity size={20} /></div></div>
                <div className="overview-grid"><section className="panel quick-panel"><div className="panel-heading"><div><div className="panel-kicker">NEXT STEP</div><h2>{scenarios.length === 0 ? 'Define your first check' : approved.length === 0 ? 'Review your scenarios' : 'Run your approved checks'}</h2></div><ArrowDownRight size={21} /></div><p>{scenarios.length === 0 ? 'Write a business outcome and browser steps. Approval makes a scenario eligible for execution.' : approved.length === 0 ? 'Your configured scenarios are unapproved. Review a copy of a draft, then approve its outcome and assertions.' : `Choose from ${approved.length} approved ${approved.length === 1 ? 'scenario' : 'scenarios'}, then launch a real browser run.`}</p><button className="button button-primary" onClick={() => { if (scenarios.length === 0) { setPage('scenarios'); openEditor() } else setPage('scenarios') }}>{scenarios.length === 0 ? 'Add scenario' : 'View scenarios'} <ArrowRight size={16} /></button></section><section className="panel latest-panel"><div className="panel-heading"><div><div className="panel-kicker">LATEST ACTIVITY</div><h2>Recent run</h2></div><MoreHorizontal size={20} /></div>{latestRun ? <><div className="latest-row"><StatusPill status={latestRun.status} /><span>{latestRun.scenarios.length} selected</span></div><p>{latestRun.mode === 'blocking' ? 'Blocking' : 'Advisory'} · {formatDate(latestRun.created_at)}</p><button className="link-button" onClick={() => { setSelectedRunId(latestRun.id); setPage('runs') }}>View report <ArrowRight size={15} /></button></> : <div className="quiet-empty">No runs yet. Your first report will appear here.</div>}</section></div>
                <div className="coverage-note"><HelpCircle size={18} /><span>Coverage counts configured scenarios in this project. It does not represent every possible app behaviour.</span></div>
                <div className="new-project-inline"><span>Need another target?</span><button onClick={openProjectForm}>Create another project <ArrowRight size={14} /></button></div>
              </>}
              {page === 'scenarios' && <><div className="page-heading"><div><div className="eyebrow">SCENARIO COVERAGE</div><h1>Scenarios</h1><p>Approved outcomes are frozen into each run. Unapproved scenarios stay visible but cannot execute.</p></div><div className="page-actions"><button className="button button-ai" onClick={() => setAiOpen(previous => !previous)}><Sparkles size={17} />Ask AI to propose</button><button className="button button-primary" onClick={() => openEditor()}><Plus size={17} />Add scenario</button></div></div>
                {aiOpen && <AiProposals key={projectId} token={token} projectId={projectId} onClose={() => setAiOpen(false)} onReview={scenario => openEditor(scenario, 'proposal')} />}
                <div className="coverage-summary"><div><span>All configured</span><strong>{scenarios.length}</strong></div><div><span className="mini-dot approved" />Approved<strong>{coverage.approved}</strong></div><div><span className="mini-dot unapproved" />Unapproved<strong>{coverage.unapproved}</strong></div><p>Coverage describes configured scenarios, not every possible app behaviour.</p></div>
                {scenarios.length === 0 ? <EmptyState icon={<ShieldCheck size={28} />} title="No scenarios yet">Add an expected business outcome and browser steps to begin your coverage.</EmptyState> : <div className="scenario-grid"><div className="scenario-list-head"><h2>Configured checks</h2><span>{selectedScenarioIds.length} selected for next run</span></div>{scenarios.map(scenario => <article className={`scenario-card ${selectedScenarioIds.includes(scenario.id) ? 'selected' : ''}`} key={scenario.id}><div className="scenario-select"><input type="checkbox" aria-label={`Select ${scenario.name} for next run`} disabled={!scenario.approved} checked={selectedScenarioIds.includes(scenario.id)} onChange={() => toggleScenario(scenario.id)} /></div><div className="scenario-main"><div className="scenario-topline"><span className={`approval-label ${scenario.approved ? 'is-approved' : ''}`}>{scenario.approved ? <Check size={13} /> : <Clock3 size={13} />}{scenario.approved ? 'APPROVED' : 'UNAPPROVED'}</span><span>#{shortId(scenario.id)}</span></div><h3>{scenario.name}</h3>{scenario.description && <p className="scenario-description">{scenario.description}</p>}<div className="expected"><span>EXPECTED OUTCOME</span><p>{scenario.expected_outcome}</p></div><div className="scenario-footer"><span>{scenario.steps.length} {scenario.steps.length === 1 ? 'step' : 'steps'}</span><span>Created {formatDate(scenario.created_at)}</span><button className="scenario-review" onClick={() => reviewCopy(scenario)}>Review a copy <ArrowRight size={13} /></button></div></div></article>)}</div>}
                {!aiOpen && approved.length > 0 && <div className="launch-bar"><div><strong>{selectedScenarioIds.length} approved {selectedScenarioIds.length === 1 ? 'scenario' : 'scenarios'} selected</strong><small>Each runs in a fresh browser context.</small></div><div className="launch-controls"><label htmlFor="scenario-mode">Mode</label><select id="scenario-mode" value={mode} onChange={event => setMode(event.target.value as RunMode)}><option value="advisory">Advisory · warn</option><option value="blocking">Blocking · fail</option></select><button className="button button-primary" disabled={launching || selectedScenarioIds.length === 0} onClick={() => void launchRun()}>{launching ? <LoaderCircle className="spin" size={16} /> : <Play size={16} />}{launching ? 'Queuing…' : 'Run checks'}</button></div></div>}
              </>}
              {page === 'repositories' && <RepositorySnapshots key={projectId} token={token} projectId={projectId} focusSnapshotId={focusSnapshotId} />}
              {page === 'discoveries' && <Discoveries key={projectId} token={token} projectId={projectId} approvedScenarios={approved} />}
              {page === 'releases' && <Releases key={projectId} token={token} projectId={projectId} onOpenSnapshot={id => { setFocusSnapshotId(id); setPage('repositories') }} />}
              {page === 'runs' && <><div className="page-heading"><div><div className="eyebrow">BROWSER EXECUTION</div><h1>Runs & results</h1><p>Track what passed, what did not match, and what could not complete.</p></div><button className="button button-outline" onClick={() => void loadProjectData(token, projectId)}><RefreshCw size={16} />Refresh</button></div>
                {runs.length === 0 ? <EmptyState icon={<Play size={28} />} title="No runs yet">Select approved scenarios and launch a browser check to see your first report.<button className="button button-primary" onClick={() => setPage('scenarios')}>Choose scenarios <ArrowRight size={16} /></button></EmptyState> : <div className="runs-layout"><aside className="runs-list"><div className="runs-list-header"><h2>Run history</h2><span>{runs.length}</span></div>{runs.map(run => <button key={run.id} className={`run-list-item ${run.id === selectedRunId ? 'active' : ''}`} onClick={() => setSelectedRunId(run.id)}><span className="run-item-top"><strong>{shortId(run.id)}</strong><StatusPill status={run.status} /></span><span>{run.scenarios.length} scenarios · {run.mode}</span><small>{formatDate(run.created_at)}</small></button>)}</aside><section className="panel report-panel">{selectedRun && selectedRun.id === selectedRunId ? <RunDetail run={selectedRun} onCancel={() => void cancelRun()} cancelling={cancelling} /> : <div className="loading-state"><LoaderCircle size={22} className="spin" />Loading report…</div>}</section></div>}
              </>}
            </>}
          </>}
        </>}
      </div>
    </main>
    {editorOpen && <ScenarioEditor token={token} projectId={projectId} initial={editorDraft} source={editorSource} onClose={closeEditor} onSaved={savedScenario} />}
  </div>
}

export default App
