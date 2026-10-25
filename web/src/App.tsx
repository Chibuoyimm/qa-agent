import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Activity, ArrowRight, ClipboardCheck, Grid2X2, MessageSquare, Plus, Settings, ShieldCheck } from 'lucide-react'
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
import './workspace.css'

type Page = 'test' | 'coverage' | 'results' | 'settings'
type Route = { page: Page; project: string; task: string; run: string; journey: string }
function readRoute(): Route {
  const [path, query] = location.hash.slice(1).split('?')
  const params = new URLSearchParams(query)
  return { page: ['test', 'coverage', 'results', 'settings'].includes(path.replace(/^\//, '')) ? path.replace(/^\//, '') as Page : 'test', project: params.get('project') || '', task: params.get('task') || '', run: params.get('run') || '', journey: params.get('journey') || '' }
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
  const [aiReady, setAIReady] = useState(false)
  const [runs, setRuns] = useState<Run[]>([])
  const [revision, setRevision] = useState(0)
  const [configRevision, setConfigRevision] = useState(0)
  const [newApp, setNewApp] = useState(false)
  const [name, setName] = useState('')
  const [url, setURL] = useState('')
  const [creating, setCreating] = useState(false)
  const [setting, setSetting] = useState('App & access')
  const [editor, setEditor] = useState<{ initial: ScenarioInput | null; source: 'new' | 'copy' | 'proposal' } | null>(null)
  const [standaloneAI, setStandaloneAI] = useState(false)
  const [savedScenario, setSavedScenario] = useState<Scenario | null>(null)
  const [snapshot, setSnapshot] = useState('')
  const [runContext, setRunContext] = useState('')
  const changed = useCallback(() => setRevision(value => value + 1), [])
  const project = projects.find(item => item.id === route.project)
  function navigate(patch: Partial<Route>) {
    if (route.page === 'settings' && patch.page && patch.page !== 'settings') setConfigRevision(value => value + 1)
    const next = { ...route, ...patch }
    const params = new URLSearchParams()
    if (next.project) params.set('project', next.project)
    if (next.task) params.set('task', next.task)
    if (next.run) params.set('run', next.run)
    if (next.journey) params.set('journey', next.journey)
    location.hash = `/${next.page}?${params}`
    setRoute(next)
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
        const next = { ...previous, project: values[0].id, task: '', run: '', journey: '' }
        const params = new URLSearchParams({ project: next.project })
        history.replaceState(null, '', `#/${next.page}?${params}`)
        return next
      })
    }).catch(err => { if (!abort.signal.aborted) setError(errorText(err)) })
    return () => abort.abort()
  }, [token])
  useEffect(() => { setScenarios([]); setJourneys([]); setRuns([]); setTasks([]); setAccess(null); setAIReady(false); setRunContext(''); setEditor(null); setStandaloneAI(false); setSavedScenario(null) }, [route.project])
  useEffect(() => {
    if (!token || !project) return
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      try {
        const [checks, tracked, attempts, requests, verified] = await Promise.all([api<Scenario[]>(token, `/api/projects/${project!.id}/scenarios`, { signal: abort.signal }), api<Journey[]>(token, `/api/projects/${project!.id}/journeys`, { signal: abort.signal }), api<Run[]>(token, `/api/projects/${project!.id}/runs`, { signal: abort.signal }), api<TestingTask[]>(token, `/api/projects/${project!.id}/tasks`, {signal: abort.signal}), api<VerifiedAccess>(token, `/api/projects/${project!.id}/access`, {signal: abort.signal})])
        if (abort.signal.aborted) return
        setScenarios(checks); setJourneys(tracked); setRuns(attempts); setTasks(requests); setAccess(verified); setError('')
        if (attempts.some(run => isActive(run.status))) timer = setTimeout(() => void load(), 2200)
      } catch (err) { if (!abort.signal.aborted) setError(errorText(err)) }
    }
    void load()
    return () => { abort.abort(); clearTimeout(timer) }
  }, [token, project, revision])
  async function createApp(event: FormEvent) {
    event.preventDefault(); setCreating(true); setError('')
    try {
      const value = await api<Project>(token, '/api/projects', { method: 'POST', body: JSON.stringify({ name, base_url: url }) })
      setProjects(previous => [...previous, value]); setNewApp(false); setName(''); setURL(''); navigate({ project: value.id, page: 'settings', task: '', run: '', journey: '' }); setSetting('App & access')
    } catch (err) { setError(errorText(err)) } finally { setCreating(false) }
  }
  function openRun(id: string) { navigate({ page: 'results', run: id }) }
  function investigate(run: Run) { setRunContext(run.id); navigate({ page: 'test', task: run.task_id || '', run: '' }) }

  if (!token) return <div className="auth-page"><div className="auth-content"><div className="brand brand-auth"><span className="brand-mark"><Activity size={25} /></span><span>qa<span className="brand-accent">agent</span></span></div><div className="auth-card"><div className="eyebrow">YOUR TEST WORKSPACE</div><h1>Know what works.</h1><p>Describe what matters, review the checks, and see the evidence from your app.</p><form onSubmit={event => { event.preventDefault(); setToken(tokenInput.trim()); setTokenInput('') }}><label className="field-label" htmlFor="api-token">Workspace access token</label><input id="api-token" type="password" autoComplete="off" required value={tokenInput} onChange={event => setTokenInput(event.target.value)} /><button className="button button-primary auth-submit" disabled={!tokenInput.trim()}>Open workspace <ArrowRight size={17} /></button></form><p className="field-help">Your operator provides this token. It stays in this tab’s memory and clears on reload.</p></div></div></div>
  return <div className="workspace-shell"><aside className="workspace-nav"><a className="workspace-brand" href={`#/test?project=${route.project}`}><Activity size={25} /><strong>qa<span>agent</span></strong></a><label className="workspace-switch">APP<select value={route.project} aria-label="Current app" onChange={event => { if (event.target.value === '__new') { setNewApp(true); return } navigate({ project: event.target.value, task: '', run: '', journey: '' }); setNewApp(false) }}>{!projects.length && <option value="">{loaded ? 'No apps yet' : 'Loading apps…'}</option>}{projects.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}<option value="__new">+ Connect another app</option></select></label><button className="link-button add-app" onClick={() => setNewApp(true)}><Plus size={14} />Add app</button><nav aria-label="Main navigation">{nav.map(({ page, label, Icon }) => <button key={page} className={route.page === page ? 'active' : ''} aria-current={route.page === page ? 'page' : undefined} onClick={() => navigate({ page })}><Icon size={18} />{label}</button>)}</nav><div className="workspace-nav-foot"><ShieldCheck size={17} /><p>Evidence for the checks you choose.</p><button className="link-button" onClick={() => { setToken(''); setLoaded(false); setProjects([]) }}>Lock workspace</button></div></aside><main className="workspace-content">
    {error && <div className="error-banner" role="alert">{error}<button className="link-button" onClick={() => { if (!loaded) { setToken(''); setError('') } else changed() }}>{!loaded ? 'Change access token' : 'Retry'}</button></div>}
    {!loaded && !error && <p>Connecting to your workspace…</p>}
    {(newApp || (loaded && projects.length === 0)) && <div className="flow-page setup-page"><div className="eyebrow">CONNECT YOUR APP</div><h1>Where should we test?</h1><p>Connect an app you control. We’ll check its access and explore its pages before you review any test plan.</p><form className="flow-card" onSubmit={event => void createApp(event)}><label htmlFor="app-name">App name</label><input id="app-name" required maxLength={200} value={name} placeholder="e.g. Acme staging" onChange={event => setName(event.target.value)} /><label htmlFor="app-url">App URL</label><input id="app-url" type="url" required placeholder="https://staging.example.com" value={url} onChange={event => setURL(event.target.value)} /><p className="muted">Use the app’s origin, without a page path. Your operator must allow it on the server and browser worker.</p><div className="flow-row">{projects.length > 0 && <button className="button button-outline" type="button" onClick={() => setNewApp(false)}>Cancel</button>}<button className="button button-primary" disabled={creating}>{creating ? 'Connecting…' : 'Connect app'}</button></div></form></div>}
    {project && !newApp && <>
      <section hidden={route.page !== 'test'}>{!access?.discovery_id && <div className="setup-nudge">App access hasn’t been verified yet. <button className="link-button" onClick={() => {navigate({page:'settings'});setSetting('App & access')}}>Finish setup</button></div>}<TaskWorkspace key={project.id} token={token} project={project} taskId={route.task} journeys={journeys} scenarios={scenarios} onTask={id => navigate({ task: id, journey: id ? route.journey : '', run: '' })} onChanged={changed} onOpenRun={openRun} initialRunContext={runContext} onContextUsed={() => setRunContext('')} configRevision={configRevision} initialJourney={route.journey} defaultSetup={access?.setup_scenario_id} onAIReady={setAIReady} onSettings={() => {navigate({page:'settings'});setSetting('App & access')}} /></section>
      {route.page === 'coverage' && <Coverage key={project.id} token={token} project={project} revision={revision} onChanged={changed} onOpenRun={openRun} onTest={journey => navigate({page:'test',task:'',run:'',journey})} />}
      {route.page === 'results' && (route.run ? <RunReport token={token} id={route.run} onBack={() => navigate({ run: '' })} onOpenRun={openRun} onInvestigate={investigate} onChanged={changed} title={runs.find(r=>r.id===route.run) ? runTitle(runs.find(r=>r.id===route.run)!,tasks,journeys) : undefined} /> : <RunHistory runs={runs} tasks={tasks} journeys={journeys} onOpen={openRun} onRefresh={changed} onTest={() => navigate({page:'test',run:''})} />)}
      {route.page === 'settings' && <div className="flow-page settings-page"><div className="page-heading"><div><div className="eyebrow">SETTINGS</div><h1>App connections & access</h1><p>Set up once, then return to Test to plan and run checks.</p></div></div><div className="flow-tabs settings-tabs">{['App & access', 'AI connections', 'Repositories', 'App exploration', 'Releases'].map(item => <button className={setting === item ? 'active' : ''} key={item} onClick={() => { if (setting === 'AI connections') setConfigRevision(value => value + 1); setSetting(item) }}>{item}</button>)}</div>
        {setting === 'App & access' && <><AppReadiness key={project.id} token={token} projectId={project.id} scenarios={scenarios} access={access} aiReady={aiReady} onVerified={value => {setAccess(value);changed()}} onAI={() => setSetting('AI connections')} onAddAccess={() => setEditor({initial:null,source:'new'})} onTest={() => navigate({page:'test',task:'',run:'',journey:''})} /><section className="flow-card"><h2>Earlier conversations</h2><p>Your conversations from the previous workspace are still available.</p><button className="link-button" onClick={() => setSetting('Earlier conversations')}>Open earlier conversations</button></section><details className="flow-card"><summary>Advanced check editor ({scenarios.length} checks)</summary><p>For engineers who want to define exact browser steps or copy an existing check. Copies are separate checks in Coverage.</p><div className="flow-row"><button className="button button-outline" onClick={() => setEditor({ initial: null, source: 'new' })}>Add a check manually</button><button className="button button-outline" onClick={() => setStandaloneAI(true)}>Ask AI to propose</button></div>{scenarios.map(s => <div className="advanced-check" key={s.id}><span>{s.name} · {s.approved ? 'Approved' : 'Draft'}</span><button className="link-button" onClick={() => setEditor({ initial: scenarioInput(s), source: 'copy' })}>Review a copy</button></div>)}</details></>}
        {setting === 'App & access' && standaloneAI && <AiProposals token={token} projectId={project.id} onClose={() => setStandaloneAI(false)} onReview={input => setEditor({ initial: input, source: 'proposal' })} />}
        {setting === 'Earlier conversations' && <ChatWorkspace token={token} project={project} scenarios={scenarios} savedScenario={savedScenario} onReview={input => setEditor({ initial: input, source: 'proposal' })} onOpenRun={openRun} onRunQueued={changed} />}
        {setting === 'AI connections' && <AiProposals key={`settings-${project.id}`} token={token} projectId={project.id} settingsOnly onReady={setAIReady} onClose={() => {}} onReview={() => {}} />}
        {setting === 'Repositories' && <RepositorySnapshots token={token} projectId={project.id} focusSnapshotId={snapshot} />}
        {setting === 'App exploration' && <Discoveries token={token} projectId={project.id} approvedScenarios={scenarios.filter(s => s.approved)} />}
        {setting === 'Releases' && <Releases token={token} projectId={project.id} onOpenSnapshot={id => { setSnapshot(id); setSetting('Repositories') }} />}
      </div>}
    </>}
  </main>{editor && project && <ScenarioEditor token={token} projectId={project.id} initial={editor.initial} source={editor.source} onClose={() => setEditor(null)} onSaved={scenario => { setSavedScenario(scenario); changed() }} />}</div>
}
