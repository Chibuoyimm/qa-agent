import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Activity, ArrowRight, ClipboardCheck, Grid2X2, MessageSquare, Plus, Settings, LockKeyhole, Layers3, ChevronDown, ChevronRight, Globe2 } from 'lucide-react'
import AppReadiness, { type VerifiedAccess } from './AppReadiness'
import RunHistory from './RunHistory'
import TaskWorkspace from './TaskWorkspace'
import ChatWorkspace from './ChatWorkspace'
import Coverage from './Coverage'
import RunReport from './RunReport'
import ScenarioEditor from './ScenarioEditor'
import AiProposals from './AiProposals'
import RepositorySnapshots from './RepositorySnapshots'
import Discoveries from './Discoveries'
import Releases from './Releases'
import { api, isActive, type TestingTask, type Journey, type Project, type Run, type Scenario, type ScenarioInput } from './api'
import { errorText, scenarioInput, runTitle } from './workspace'

type Page = 'connect' | 'test' | 'coverage' | 'results' | 'settings'
const settingsTabs = ['App & access', 'AI connections', 'Repositories', 'App exploration', 'Releases', 'Earlier conversations'] as const
type Setting = typeof settingsTabs[number]
type Route = { page: Page; project: string; task: string; run: string; journey: string; context: string; draftRun: string; setting: Setting; snapshot: string }

function readRoute(): Route {
  const [path, query] = location.hash.slice(1).split('?')
  const params = new URLSearchParams(query)
  const page = path.replace(/^\//, '')
  const task = params.get('task') || ''
  const journey = params.get('journey') || ''
  const context = params.get('context') || ''
  // Existing links with an attached standalone run already own a separate draft.
  const draftRun = task || journey ? '' : params.get('draftRun') || context
  const setting = settingsTabs.find(tab => tab === params.get('tab')) ?? 'App & access'
  return {
    page: ['connect', 'test', 'coverage', 'results', 'settings'].includes(page) ? page as Page : 'test',
    project: params.get('project') || '', task, journey, context, draftRun, setting,
    run: params.get('run') || '',
    snapshot: page === 'settings' && setting === 'Repositories' ? params.get('snapshot') || '' : '',
  }
}

function routeHash(route: Route) {
  const params = new URLSearchParams()
  for (const key of ['project', 'task', 'run', 'context', 'journey'] as const) {
    if (route[key]) params.set(key, route[key])
  }
  if (route.draftRun && route.draftRun !== route.context) params.set('draftRun', route.draftRun)
  if (route.setting !== 'App & access') params.set('tab', route.setting)
  if (route.snapshot) params.set('snapshot', route.snapshot)
  return `#/${route.page}?${params}`
}
const nav = [{ page: 'test', label: 'Test', Icon: MessageSquare }, { page: 'coverage', label: 'Coverage', Icon: Grid2X2 }, { page: 'results', label: 'Results', Icon: ClipboardCheck }, { page: 'settings', label: 'Settings', Icon: Settings }] as const

export default function App() {
  const [route, setRoute] = useState<Route>(readRoute)
  const [token, setToken] = useState('')
  const [tokenInput, setTokenInput] = useState('')
  const [projects, setProjects] = useState<Project[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  const [scenarios, setScenarios] = useState<Scenario[]>([])
  const [journeys, setJourneys] = useState<Journey[]>([])
  const [tasks, setTasks] = useState<TestingTask[]>([])
  const [access, setAccess] = useState<VerifiedAccess | null>(null)
  const [aiReady, setAIReady] = useState<'ready' | 'per_request' | 'unavailable'>('unavailable')
  const [runs, setRuns] = useState<Run[]>([])
  const [revision, setRevision] = useState(0)
  const [configRevision, setConfigRevision] = useState(0)
  const newApp = route.page === 'connect'
  const [name, setName] = useState('')
  const [url, setURL] = useState('')
  const [creating, setCreating] = useState(false)
  const setting = route.setting
  const [editor, setEditor] = useState<{ initial: ScenarioInput | null; source: 'new' | 'copy' | 'proposal' } | null>(null)
  const [standaloneAI, setStandaloneAI] = useState(false)
  const [savedScenario, setSavedScenario] = useState<Scenario | null>(null)
  const [returnedSource,setReturnedSource] = useState<{id:string;scope:string} | null>(null)
  const [discussedRun, setDiscussedRun] = useState<Run | null>(null)
  const [sourceRequest, setSourceRequest] = useState<{scope: string; prompt: string; connect?:boolean} | null>(null)
  let draftId = 'new'
  if (route.task) draftId = route.task
  else if (route.journey) draftId = 'journey:' + route.journey
  else if (route.draftRun) draftId = 'run:' + route.draftRun
  const draftKey = `qa-task-draft:${route.project}:${draftId}`
  const currentSearch = !route.snapshot && sourceRequest?.scope === draftKey ? sourceRequest : null
  const changed = useCallback(() => setRevision(value => value + 1), [])
  const project = projects.find(item => item.id === route.project)
  function navigate(patch: Partial<Route>) {
    if (route.page === 'settings' && patch.page && patch.page !== 'settings') setConfigRevision(value => value + 1)
    const changingScope = patch.project !== undefined || patch.task !== undefined || patch.journey !== undefined
    const next = { ...readRoute(), ...(changingScope ? { context: '', draftRun: '' } : {}), ...patch }
    if (next.page !== 'settings' || next.setting !== 'Repositories' || patch.project !== undefined) next.snapshot = ''
    location.hash = routeHash(next)
    setRoute(next)
  }
  function openSettings(setting: Setting) {
    navigate({ page: 'settings', setting, snapshot: '' })
  }
  function snapshotHref(id: string) {
    return routeHash({ ...route, page: 'settings', setting: 'Repositories', snapshot: id })
  }
  useEffect(() => { const update = () => setRoute(readRoute()); window.addEventListener('hashchange', update); return () => window.removeEventListener('hashchange', update) }, [])
  useEffect(() => {
    if (!token) return
    const abort = new AbortController()
    setError('')
    void api<Project[]>(token, '/api/projects', { signal: abort.signal }).then(values => {
      if (abort.signal.aborted) return
      setProjects(values); setLoaded(true)
      setRoute(previous => {
        if (values.some(item => item.id === previous.project) || !values.length) return previous
        const next: Route = { ...previous, project: values[0].id, context: '', draftRun: '', task: '', run: '', journey: '', setting: 'App & access', snapshot: '' }
        history.replaceState(null, '', routeHash(next))
        return next
      })
    }).catch(err => { if (!abort.signal.aborted) setError(errorText(err)) })
    return () => abort.abort()
  }, [token])
  useEffect(() => { setScenarios([]); setJourneys([]); setRuns([]); setTasks([]); setAccess(null); setAIReady('unavailable'); setDiscussedRun(null);setSourceRequest(null);setReturnedSource(null); setEditor(null); setStandaloneAI(false); setSavedScenario(null) }, [route.project])
  useEffect(() => {
    if (!token || !project) return
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      try {
        const [checks, tracked, attempts, requests, verified] = await Promise.all([api<Scenario[]>(token, `/api/projects/${project!.id}/scenarios`, { signal: abort.signal }), api<Journey[]>(token, `/api/projects/${project!.id}/journeys`, { signal: abort.signal }), api<Run[]>(token, `/api/projects/${project!.id}/runs`, { signal: abort.signal }), api<TestingTask[]>(token, `/api/projects/${project!.id}/tasks?for=results`, {signal: abort.signal}), api<VerifiedAccess>(token, `/api/projects/${project!.id}/access`, {signal: abort.signal})])
        if (abort.signal.aborted) return
        setScenarios(checks); setJourneys(tracked); setRuns(attempts); setTasks(requests); setAccess(verified); setError('')
        if (['queued','running'].includes(verified.verification_status || '') || attempts.some(run => isActive(run.status))) timer = setTimeout(() => void load(), 2200)
      } catch (err) { if (!abort.signal.aborted) setError(errorText(err)) }
    }
    void load()
    return () => { abort.abort(); clearTimeout(timer) }
  }, [token, project, revision])
  async function createApp(event: FormEvent) {
    event.preventDefault(); setCreating(true); setError('')
    try {
      const value = await api<Project>(token, '/api/projects', { method: 'POST', body: JSON.stringify({ name, base_url: url }) })
      setProjects(previous => [...previous, value]); setName(''); setURL(''); navigate({ project: value.id, page: 'settings', setting: 'App & access', task: '', run: '', journey: '' })
    } catch (err) { setError(errorText(err)) } finally { setCreating(false) }
  }
  function openRun(id: string) { navigate({ page: 'results', run: id }) }
  function investigate(run: Run) { setDiscussedRun(run); navigate({ page: 'test', context:run.id, draftRun: run.task_id ? '' : run.id, journey:'',task: run.task_id || '', run: '' }) }

  const brand = <><span className="brand-mark"><Activity aria-hidden="true" size={22} strokeWidth={2.4} /></span><strong>QA Agent</strong></>
  if (!token) return <main className="auth-page"><div className="auth-content">
    <div className="workspace-brand brand-auth">{brand}</div>
    <section className="auth-card"><h1>Open your workspace</h1><p>Enter your access token to review requests, run checks and inspect results.</p>
      <form onSubmit={event => { event.preventDefault(); setToken(tokenInput.trim()); setTokenInput('') }}>
        <label className="field-label" htmlFor="api-token">Workspace access token</label>
        <input id="api-token" type="password" autoComplete="off" required value={tokenInput} onChange={event => setTokenInput(event.target.value)} />
        <button className="button button-primary auth-submit" disabled={!tokenInput.trim()}>Open workspace <ArrowRight size={16} /></button>
      </form><p className="auth-foot"><LockKeyhole aria-hidden="true" size={16} />Your operator provides this token. It clears when you reload the page.</p>
    </section>
  </div></main>
  const pageName = nav.find(item => item.page === route.page)?.label ?? 'Connect app'
  return <div className="workspace-shell">
    <a className="skip-content" href="#workspace-main" onClick={event => {event.preventDefault();document.getElementById('workspace-main')?.focus()}}>Skip to workspace</a>
    <aside className="workspace-nav">
      <a className="workspace-brand" href={`#/test?project=${route.project}`}>{brand}</a>
      <div className="workspace-switch"><label htmlFor="workspace-app">Application</label>
        <div className="app-picker"><Layers3 aria-hidden="true" size={17} /><select id="workspace-app" value={route.project} aria-label="Current app" onChange={event => { if (event.target.value === '__new') { navigate({page:'connect'}); return } navigate({project:event.target.value, task:'', run:'', journey:''}) }}>
          {!projects.length && <option value="">{loaded ? 'No apps yet' : 'Loading apps…'}</option>}
          {projects.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}<option value="__new">+ Connect another app</option>
        </select><ChevronDown size={13} aria-hidden="true" /></div>
      </div>
      <nav aria-label="Main navigation">{nav.map(({page,label,Icon}) => <button key={page} className={route.page === page ? 'active' : ''} aria-current={route.page === page ? 'page' : undefined} onClick={() => navigate({page})}><Icon aria-hidden="true" size={18}/>{label}</button>)}</nav>
      <div className="workspace-nav-foot"><button className="add-app" onClick={() => navigate({page:'connect'})}><Plus size={17}/>Add app</button>
        <button aria-label="Lock workspace" title="Lock workspace" onClick={() => {setToken('');setLoaded(false);setProjects([])}}><LockKeyhole size={16}/><span>Lock workspace</span></button></div>
    </aside>
    <main className="workspace-content" id="workspace-main" tabIndex={-1}>
      <header className="workspace-bar"><div className="workspace-location"><Layers3 size={16} aria-hidden="true"/><span>{project?.name || 'Workspace'}</span><ChevronRight size={14} aria-hidden="true"/><strong>{pageName}</strong></div>
        {project && route.page === 'test' && <span className="workspace-target"><Globe2 size={14} aria-hidden="true"/><span>Test target</span><span>{project.base_url}</span></span>}
      </header>
    {error && <div className="error-banner" role="alert">{error}<button className="link-button" onClick={() => { if (!loaded) { setToken(''); setError('') } else changed() }}>{!loaded ? 'Change access token' : 'Retry'}</button></div>}
    {!loaded && !error && <p>Connecting to your workspace…</p>}
    {(newApp || (loaded && projects.length === 0)) && <div className="flow-page setup-page"><h1>Where should we test?</h1><p>Connect an app you control. We’ll check its access and explore its pages before you review any test plan.</p><form className="flow-card" onSubmit={event => void createApp(event)}><label htmlFor="app-name">App name</label><input id="app-name" required maxLength={200} value={name} placeholder="e.g. Acme staging" onChange={event => setName(event.target.value)} /><label htmlFor="app-url">App URL</label><input id="app-url" type="url" required placeholder="https://staging.example.com" value={url} onChange={event => setURL(event.target.value)} /><p className="muted">Use the app’s origin, without a page path. Your operator must allow it on the server and browser worker.</p><div className="flow-row">{projects.length > 0 && <button className="button button-outline" type="button" onClick={() => navigate({page:'test'})}>Cancel</button>}<button className="button button-primary" disabled={creating}>{creating ? 'Connecting…' : 'Connect app'}</button></div></form></div>}
    {project && !newApp && <>
      <section hidden={route.page !== 'test'}><TaskWorkspace key={project.id} token={token} project={project} taskId={route.task} draftKey={draftKey} journeys={journeys} scenarios={scenarios} onTask={(id, keepAttachment) => navigate({ task: id, context: keepAttachment ? route.context : '', journey: id ? route.journey : '', run: '' })} onChanged={changed} onOpenRun={openRun} initialRunContext={route.context} attachedRun={runs.find(r => r.id === route.context) ?? (discussedRun?.id === route.context ? discussedRun : null)} onContextUsed={() => {if(readRoute().context === route.context) navigate({context:''})}} configRevision={configRevision} initialJourney={route.journey} accessVerified={Boolean(access?.discovery_id)} defaultSetup={access?.setup_scenario_id} sourceToReview={returnedSource?.scope === draftKey ? returnedSource.id : ''} onAIReady={setAIReady} onFindSource={(prompt,connect) => {setSourceRequest({scope:draftKey,prompt,connect});openSettings('Repositories')}} onAISettings={() => openSettings('AI connections')} onSettings={() => openSettings('App & access')} /></section>
      {route.page === 'coverage' && <Coverage key={project.id} token={token} project={project} revision={revision} onChanged={changed} onOpenRun={openRun} onTest={journey => navigate({page:'test',task:'',run:'',journey})} />}
      {route.page === 'results' && (route.run ? <RunReport token={token} id={route.run} snapshotHref={snapshotHref} onBack={() => navigate({ run: '' })} onOpenRun={openRun} onInvestigate={investigate} onChanged={changed} title={runs.find(r=>r.id===route.run) ? runTitle(runs.find(r=>r.id===route.run)!,tasks,journeys) : undefined} /> : <RunHistory runs={runs} tasks={tasks} journeys={journeys} onOpen={openRun} onRefresh={changed} onTest={() => navigate({page:'test',run:''})} />)}
      {route.page === 'settings' && <div className="flow-page settings-page"><div className="page-heading"><div><h1>App connections & access</h1><p>Set up once, then return to Test to plan and run checks.</p></div></div><div className="flow-tabs settings-tabs">{settingsTabs.filter(item => item !== 'Earlier conversations').map(item => <button className={setting === item ? 'active' : ''} key={item} aria-pressed={setting === item} onClick={() => { setSourceRequest(null); if (setting === 'AI connections') setConfigRevision(value => value + 1); openSettings(item) }}>{item}</button>)}</div>
        {setting === 'App & access' && <><AppReadiness key={project.id} token={token} projectId={project.id} scenarios={scenarios} access={access} aiReady={aiReady} onVerificationStarted={changed} handoffURL={location.href} projectName={project.name} target={project.base_url} onAI={() => openSettings('AI connections')}  onTest={() => navigate({page:'test'})} /><details className="flow-card"><summary>Advanced check editor ({scenarios.length} checks)</summary><p>For engineers who want to define exact browser steps or copy an existing check. Copies are separate checks in Coverage.</p><div className="flow-row"><button className="button button-outline" onClick={() => setEditor({ initial: null, source: 'new' })}>Add a check manually</button><button className="button button-outline" onClick={() => setStandaloneAI(true)}>Ask AI to propose</button></div>{scenarios.map(s => <div className="advanced-check" key={s.id}><span>{s.name} · {s.approved ? 'Approved' : 'Draft'}</span><button className="link-button" onClick={() => setEditor({ initial: scenarioInput(s), source: 'copy' })}>Review a copy</button></div>)}</details><section className="flow-card"><h2>Earlier conversations</h2><p>Your conversations from the previous workspace are still available.</p><button className="link-button" onClick={() => openSettings('Earlier conversations')}>Open earlier conversations</button></section></>}
        {setting === 'App & access' && standaloneAI && <AiProposals token={token} projectId={project.id} onClose={() => setStandaloneAI(false)} onReview={input => setEditor({ initial: input, source: 'proposal' })} />}
        {setting === 'Earlier conversations' && <ChatWorkspace token={token} project={project} scenarios={scenarios} savedScenario={savedScenario} onReview={input => setEditor({ initial: input, source: 'proposal' })} onOpenRun={openRun} onRunQueued={changed} />}
        {setting === 'AI connections' && <AiProposals key={`settings-${project.id}`} token={token} projectId={project.id} settingsOnly onReady={setAIReady} onClose={() => {}} onReview={() => {}} />}
        {setting === 'Repositories' && <RepositorySnapshots token={token} projectId={project.id} focusSnapshotId={route.snapshot} requestPrompt={currentSearch?.prompt} connect={currentSearch?.connect} onReturn={!currentSearch ? undefined : id => {setReturnedSource({id,scope:currentSearch.scope});setSourceRequest(null);navigate({page:'test'})}} />}
        {setting === 'App exploration' && <Discoveries token={token} projectId={project.id} approvedScenarios={scenarios.filter(s => s.approved)} />}
        {setting === 'Releases' && <Releases token={token} projectId={project.id} snapshotHref={snapshotHref} />}
      </div>}
    </>}
  </main>{editor && project && <ScenarioEditor token={token} projectId={project.id} initial={editor.initial} source={editor.source} onClose={() => setEditor(null)} onSaved={scenario => { setSavedScenario(scenario); changed() }} />}</div>
}
