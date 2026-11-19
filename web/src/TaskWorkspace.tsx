import { useEffect, useRef, useState } from 'react'
import { ArrowRight, LoaderCircle, Play, Plus, Sparkles, History, MessageSquareText } from 'lucide-react'
import WholeAppReview from './WholeAppReview'
import JourneyProgress from './JourneyProgress'
import PlanningRecovery from './PlanningRecovery'
import AiProposals from './AiProposals'
import ReviewPlan, { type PlanRunSettings } from './ReviewPlan'
import { api, type SourceSurvey, type ProjectSource, formatDate, isActive, newRequestID, type ChatPage, type ChatTurn, type Discovery, type Journey, type Project, type Run, type Scenario, type TestingTask } from './api'
import { noProjectRepositories, errorText, runCounts, checkCount, shortTitle, journeySummary } from './workspace'

export function RunCard({ token, id, onOpen }: { token: string; id: string; onOpen: (id: string) => void }) {
  const [run, setRun] = useState<Run | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      try {
        const value = await api<Run>(token, `/api/runs/${id}`, { signal: abort.signal })
        if (abort.signal.aborted) return
        setRun(value); setError('')
        if (isActive(value.status)) timer = setTimeout(() => void load(), 1800)
      } catch (err) { if (!abort.signal.aborted) { setError(errorText(err)); timer = setTimeout(() => void load(), 5000) } }
    }
    void load()
    return () => { abort.abort(); clearTimeout(timer) }
  }, [id, token])
  const counts = run ? runCounts(run) : null
  return <section className="flow-card run-summary"><div className="flow-row"><strong>{run ? `${checkCount(run.scenarios.length)} selected` : 'Loading run…'}</strong><span className={`pill pill-${run?.status ?? 'queued'}`}>{run?.status ?? 'loading'}</span></div>{counts && <p>{counts.passed} passed · {counts.failed} failed · {counts.blocked + counts.error} unverified · {counts.incomplete} unfinished</p>}{run && <><p className="muted">{run.base_url}</p><JourneyProgress run={run}/></>}{error && <p role="alert">{error}</p>}<button className="link-button" onClick={() => onOpen(id)}>Open results and evidence <ArrowRight size={15} /></button></section>
}

export default function TaskWorkspace({ token, project, taskId, draftKey, journeys, scenarios, onTask, onChanged, onOpenRun, initialRunContext, attachedRun, onContextUsed, configRevision, initialJourney = '', defaultSetup = '', accessVerified, sourceToReview, onFindSource, onAIReady, onSettings, onAISettings }: { token: string; project: Project; taskId: string; draftKey: string; journeys: Journey[]; scenarios: Scenario[]; onTask: (id: string, keepAttachment?: boolean) => void; onChanged: () => void; onOpenRun: (id: string) => void; initialRunContext: string; attachedRun: Run | null; accessVerified: boolean; sourceToReview: string; onFindSource: (prompt:string, connect?:boolean) => void; onContextUsed: () => void; configRevision: number; initialJourney?: string; defaultSetup?: string; onAIReady: (ready: 'ready' | 'per_request' | 'unavailable') => void; onSettings: () => void; onAISettings: () => void }) {
  const [tasks, setTasks] = useState<TestingTask[]>([])
  const [task, setTask] = useState<TestingTask | null>(null)
  const [turns, setTurns] = useState<ChatTurn[]>([])
  const [hasOlder, setHasOlder] = useState(false)
  const [showHistory, setShowHistory] = useState(false)
  const historyPanel = useRef<HTMLElement>(null)
  const historyToggle = useRef<HTMLButtonElement>(null)
  const workspaceHeading = useRef<HTMLHeadingElement>(null)
  useEffect(() => {
    if (showHistory) (historyPanel.current?.querySelector<HTMLButtonElement>('button:enabled') ?? historyPanel.current)?.focus()
  }, [showHistory])
  const [journey, setJourney] = useState(initialJourney)
  const [prefill, setPrefill] = useState<{text: string; id: number} | undefined>()
  const selectedJourneyName = journeys.find(j => j.id === initialJourney)?.name
  useEffect(() => { setJourney(initialJourney);setJourneyName('');setPrefill(undefined) }, [initialJourney, taskId])
  const [journeyName, setJourneyName] = useState('')
  const [explore, setExplore] = useState(true)
  const [setup, setSetup] = useState(defaultSetup)
  useEffect(() => setSetup(defaultSetup), [defaultSetup])
  const [mode, setMode] = useState<'advisory' | 'blocking'>('advisory')
  const [progress, setProgress] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [revision, setRevision] = useState(0)
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  const currentTask = useRef(taskId)
  currentTask.current = taskId
  const taskRequest = useRef(newRequestID())
  const busy = Boolean(progress || turns.some(turn => turn.status === 'pending'))
  useEffect(() => {
    if (showHistory && document.activeElement === historyPanel.current) {
      historyPanel.current?.querySelector<HTMLButtonElement>('button:enabled')?.focus()
    }
  }, [showHistory, tasks.length, busy])
  const root = `/api/projects/${project.id}`
  useEffect(() => {
    const abort = new AbortController()
    void api<TestingTask[]>(token, `${root}/tasks`, { signal: abort.signal }).then(setTasks).catch(err => { if (!abort.signal.aborted) setError(errorText(err)) })
    return () => abort.abort()
  }, [root, token, revision])
  useEffect(() => {
    setError(''); setTurns([]); setTask(null); setHasOlder(false)
    if (!taskId) { setLoading(false); return }
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    setLoading(true)
    async function load() {
      try {
        const [value, chat] = await Promise.all([api<TestingTask>(token, `${root}/tasks/${taskId}`, { signal: abort.signal }), api<ChatPage>(token, `${root}/tasks/${taskId}/chat`, { signal: abort.signal })])
        if (abort.signal.aborted) return
        setTask(value); setTurns(previous => { const merged = new Map(previous.map(turn => [turn.id, turn])); for (const turn of chat.turns) merged.set(turn.id, turn); return [...merged.values()].sort((a, b) => a.sequence - b.sequence) }); setHasOlder(chat.has_older); setError('')
        // Also resumes discovery progress and newly completed runs after a reload.
        timer = setTimeout(() => void load(), 5000)
      } catch (err) { if (!abort.signal.aborted) { setError(errorText(err)); timer = setTimeout(() => void load(), 8000) } } finally { if (!abort.signal.aborted) setLoading(false) }
    }
    void load()
    return () => { abort.abort(); clearTimeout(timer) }
  }, [taskId, root, token])

  async function refresh(id: string) {
    const [value, chat] = await Promise.all([api<TestingTask>(token, `${root}/tasks/${id}`), api<ChatPage>(token, `${root}/tasks/${id}/chat`)])
    if (currentTask.current === id) { setTask(value); setTurns(chat.turns); setHasOlder(chat.has_older) }
    setRevision(value => value + 1); onChanged()
  }
  async function ensureTask(prompt: string, signal?: AbortSignal) {
    if (currentTask.current) return currentTask.current
    const value = await api<TestingTask>(token, `${root}/tasks`, { method: 'POST', signal, body: JSON.stringify({ request_id: taskRequest.current, prompt, title: journeys.find(item => item.id === journey)?.name || journeyName.trim() || shortTitle(prompt), ...(journey ? { journey_id: journey } : { journey_name: journeyName.trim() || shortTitle(prompt) }) }) })
    if (!alive.current || signal?.aborted) throw new DOMException('Request interrupted', 'AbortError')
    try { const draft = sessionStorage.getItem(draftKey); if (draft) sessionStorage.setItem(`qa-task-draft:${project.id}:${value.id}`, draft); sessionStorage.removeItem(draftKey) } catch { /* The task is still saved on the server. */ }
    currentTask.current = value.id; setTask(value); onTask(value.id,true); setRevision(value => value + 1); onChanged()
    taskRequest.current = newRequestID()
    return value.id
  }
  async function send(prompt: string, options: RequestInit) {
    setProgress('Saving your request…'); setError('')
    let id = ''
    try {
      id = await ensureTask(prompt, options.signal ?? undefined)
      const input = JSON.parse(String(options.body)) as Record<string, unknown>
      if (explore && !input.whole_app && !input.fixture_id && !input.discovery_id) {
        setProgress('Exploring up to 3 pages to find paths and controls…')
        let discovery = await api<Discovery>(token, `${root}/tasks/${id}/discovery`, { method: 'POST', signal: options.signal, body: JSON.stringify({ start_path: '/', max_pages: 3, ...(setup ? { setup_scenario_id: setup } : {}) }) })
        if (discovery.status === 'error' || discovery.status === 'cancelled') discovery = await api<Discovery>(token, `${root}/tasks/${id}/discovery`, { method: 'POST', signal: options.signal, body: JSON.stringify({ start_path: '/', max_pages: 3, refresh: true, ...(setup ? { setup_scenario_id: setup } : {}) }) })
        const deadline = Date.now() + 150000
        while (discovery.status === 'queued' || discovery.status === 'running') {
          options.signal?.throwIfAborted()
          if (Date.now() > deadline) throw new Error('App exploration is still waiting for a worker. Your task is saved; check Settings → App exploration, then send your message again.')
          await new Promise<void>((resolve, reject) => {
            const signal = options.signal
            const abort = () => { clearTimeout(timer); reject(new DOMException('Cancelled', 'AbortError')) }
            const timer = setTimeout(() => { signal?.removeEventListener('abort', abort); resolve() }, 1500)
            signal?.addEventListener('abort', abort, { once: true })
          })
          discovery = await api<Discovery>(token, `/api/discoveries/${discovery.id}`, { signal: options.signal })
        }
        if (discovery.status !== 'completed') throw new Error(discovery.error || 'App exploration did not complete. Check app access or disable exploration to supply your own context.')
      }
      let batchKey = ''
      if (input.whole_app) {
        setProgress('Finding project repositories…')
        const sources = await api<ProjectSource[]>(token, `${root}/repository-sources`, { signal: options.signal })
        if (!sources.length) throw new Error(noProjectRepositories)
        batchKey = `qa-app-survey:${project.id}:${id}`
        const identity = JSON.stringify([prompt, input.provider, input.model, input.credential_mode,
          input.chatgpt_profile_id, sources.map(source => [source.id, source.ref, source.role])])
        let batch = newRequestID()
        try {
          const saved = JSON.parse(sessionStorage.getItem(batchKey) || 'null')
          if (saved?.identity === identity && /^[a-f0-9]{32}$/.test(saved.batch)) batch = saved.batch
          sessionStorage.setItem(batchKey, JSON.stringify({ identity, batch }))
        } catch { /* A new request still works if tab storage is unavailable. */ }
        for (const [index, source] of sources.entries()) {
          setProgress(`Inspecting repository ${index + 1} of ${sources.length} · ${source.repository}`)
          const endpoint = `${root}/tasks/${id}/source-surveys/${batch}/${source.id}`
          let survey = await api<SourceSurvey>(token, endpoint, options)
          while (survey.status === 'scanning') {
            setProgress(`${source.repository} · reading batch ${(survey.batches_read ?? 0) + 1} of ${survey.batches}`)
            survey = await api<SourceSurvey>(token, endpoint, options)
          }
        }
        input.survey_batch = batch
      }
      setProgress(input.whole_app ? 'Combining repository journeys into one plan…' : 'Reading the context and preparing checks…')
      await api<ChatTurn>(token, `${root}/tasks/${id}/chat`, { ...options, body: JSON.stringify({ ...input, request_id: newRequestID(), ...(initialRunContext ? { run_context_id: initialRunContext } : {}) }) })
      if (batchKey) { try { sessionStorage.removeItem(batchKey) } catch { /* Optional resume cache. */ } }
      if (initialRunContext) onContextUsed()
    } finally { setProgress(''); if (id && alive.current) await refresh(id).catch(err => setError(errorText(err))) }
  }
  async function launch(prompt: string, action: string, signal?: AbortSignal, ids?: string[], requestID = newRequestID(), settings?: PlanRunSettings) {
    setProgress('Queuing approved checks…'); setError('')
    let id = ''
    try {
      id = await ensureTask(prompt, signal)
      const source = initialRunContext || [...turns].reverse().find(turn => turn.run_id)?.run_id
      if (action === 'rerun_failed' && !source) throw new Error('Open an earlier run in Results to recheck its approved checks.')
      const turn = await api<ChatTurn>(token, `${root}/tasks/${id}/runs`, { method: 'POST', signal, body: JSON.stringify({ request_id: requestID, prompt, action, mode: settings?.mode ?? mode, ...(settings && settings.target !== project.base_url ? { base_url: settings.target } : {}), ...(ids ? { scenario_ids: ids } : {}), ...(action === 'rerun_failed' ? { source_run_id: source } : {}) }) })
      if (currentTask.current === id) setTurns(previous => [...previous.filter(item => item.id !== turn.id), turn])
      if (action === 'rerun_failed' && initialRunContext) onContextUsed()
      onChanged()
    } finally { setProgress(''); if (id && alive.current) await refresh(id).catch(err => setError(errorText(err))) }
  }
  async function older() {
    if (!taskId || !turns.length) return
    try {
      const page = await api<ChatPage>(token, `${root}/tasks/${taskId}/chat?before=${turns[0].sequence}`)
      setTurns(previous => [...page.turns.filter(turn => !previous.some(item => item.id === turn.id)), ...previous]); setHasOlder(page.has_older)
    } catch (err) { setError(errorText(err)) }
  }
  const reviewingPlan = turns.some(turn => Boolean(turn.proposal?.scenarios.length))
  useEffect(() => setShowHistory(false), [taskId, reviewingPlan])
  return <div className={`flow-page testing-layout${reviewingPlan ? ' reviewing-plan' : ''}`}>
    <header className="page-heading testing-heading"><div><h1 ref={workspaceHeading} tabIndex={-1}>{journeys.find(item => item.id === task?.journey_id)?.name || task?.title || 'What would you like to test?'}</h1></div>
      <div className="heading-actions"><button ref={historyToggle} className="button button-outline history-toggle" aria-expanded={showHistory} aria-controls="recent-requests" onClick={() => setShowHistory(!showHistory)}><History size={16}/>Recent requests</button>
        <button className="button button-outline" disabled={busy} onClick={() => {onTask(''); setShowHistory(false); taskRequest.current = newRequestID()}}><Plus size={16}/>New test request</button></div>
    </header>
    <div className="testing-main">
    {error && <div className="error-banner" role="alert">{error}</div>}{loading && <p role="status">Loading saved conversation…</p>}
    <div className="chat-thread">{hasOlder && <button className="link-button" onClick={() => void older()}>Load earlier messages</button>}{turns.map(turn => <article className="chat-exchange" key={turn.id}><div className="chat-user-message"><small>You · {formatDate(turn.created_at)}</small>{turn.proposal?.scenarios.length && turn.prompt.length > 450 ? <details className="request-preview"><summary>{turn.prompt.slice(0,150).replace(/\s+\S*$/, '')}… <span>Read full request</span></summary><p>{turn.prompt}</p></details> : <p>{turn.prompt}</p>}</div><div className="chat-agent-message"><div className="chat-agent-label"><Sparkles size={17} /><strong>QA Agent</strong>{turn.proposal && <small>{turn.proposal.model}</small>}</div>{turn.status === 'error' ? <PlanningRecovery turn={turn} disabled={busy} onRetry={text => setPrefill({text, id: Date.now()})} onSettings={onAISettings} /> : <p>{turn.status === 'pending' ? 'Preparing your test plan…' : turn.reply}</p>}{turn.proposal && <><WholeAppReview proposal={turn.proposal}/>{turn.proposal.questions.length > 0 && <div className="chat-questions"><strong>A few details before we test</strong><ol>{turn.proposal.questions.map((question, i) => <li key={i}>{question}</li>)}</ol></div>}{turn.proposal.assumptions.length > 0 && <details className="chat-assumptions"><summary>Assumptions to review ({turn.proposal.assumptions.length})</summary><ul>{turn.proposal.assumptions.map((item, i) => <li key={i}>{item}</li>)}</ul></details>}{task && <ReviewPlan defaultMode={mode} token={token} project={project} task={task} turn={turn} scenarios={scenarios} busy={busy} onApproved={() => refresh(task.id)} onRevise={text => setPrefill({text, id: Date.now()})} onRun={(ids, requestID, settings) => launch('Run the reviewed plan', 'selected', undefined, ids, requestID, settings)} />}{(turn.proposal.discovery_id || turn.proposal.repository_snapshots?.length) ? <details className="chat-assumptions"><summary>Context used for this plan</summary>{turn.proposal.discovery_id && <p>Observed app pages from discovery {turn.proposal.discovery_id.slice(0, 8)}</p>}{turn.proposal.repository_snapshots?.map(source => <p key={source.id}>{source.repository} · {source.role} · {source.commit_sha.slice(0, 12)}</p>)}</details> : null}</>}{turn.run_id && <RunCard token={token} id={turn.run_id} onOpen={onOpenRun} />}</div></article>)}</div>
    {progress && <div className="flow-progress" role="status"><LoaderCircle className="spin" size={18} />{progress}</div>}

    {initialRunContext && <div className="flow-card">Attached run · {attachedRun ? `${attachedRun.scenarios[0]?.name || 'App checks'} · ${attachedRun.status} · ${attachedRun.base_url} · ${formatDate(attachedRun.created_at)}` : initialRunContext}<button className="link-button" onClick={onContextUsed}>Remove run context</button></div>}
    <AiProposals targetOrigin={project.base_url} token={token} projectId={project.id} configRevision={configRevision} onReady={onAIReady} onClose={() => {}} onReview={() => {}} conversation={{ prefill, disabled: busy || loading, draftKey, runContextId: initialRunContext, sourceToReview, initialPrompt: !taskId && selectedJourneyName ? `Test the ${selectedJourneyName}${/journey$/i.test(selectedJourneyName) ? '' : ' journey'}.` : !loading && task && turns.length === 0 ? task.request : undefined, onFindSource: (prompt,connect) => onFindSource(prompt || task?.request || '',connect), onSettings: onAISettings, onAccessSetup: accessVerified ? undefined : async text => {
      if (text.trim() && !taskId) {
        setProgress('Saving your request…')
        try {await ensureTask(text.trim())} finally {setProgress('')}
      }
      onSettings()
    }, taskContext: true, send, command: (prompt, action, signal) => launch(prompt, action, signal) }} />
    <div className="flow-row workspace-footer"><p>Run the app’s existing checks with their approved expectations.</p><button className="link-button" disabled={busy || !scenarios.some(s => s.approved)} onClick={() => void launch('Run all approved checks', 'all_approved').catch(err => setError(errorText(err)))}>Run all approved checks ({scenarios.filter(s => s.approved).length}) <Play size={14} /></button></div>
    {!taskId && <details className="flow-card journey-options"><summary>Journey · {journeys.find(item => item.id === journey)?.name || journeyName || 'New journey'}</summary><div className="task-options"><label>Journey<select value={journey} onChange={event => setJourney(event.target.value)}><option value="">New journey</option>{journeys.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>{!journey && <label>Journey name <span className="muted">(optional)</span><input maxLength={200} placeholder="e.g. Checkout" value={journeyName} onChange={event => setJourneyName(event.target.value)} /></label>}</div></details>}
    <details className="flow-card exploration-options"><summary>App access & exploration</summary><label className="inline-check"><input type="checkbox" checked={explore} disabled={busy} onChange={event => setExplore(event.target.checked)} />Explore up to 3 app pages before planning</label><p>Discovery follows same-origin links. Its observations help find controls; your requirements establish the expected outcome.</p><label>Sign-in or access setup<select value={setup} disabled={busy} onChange={event => { setSetup(event.target.value) }}><option value="">No setup (public app)</option>{scenarios.filter(s => s.approved && !journeySummary(s.steps).phases.length && !journeySummary(s.steps).fixture).map(s => <option key={s.id} value={s.id}>{s.name}</option>)}</select></label><p>Your verified default is reused for new requests. <button className="link-button" onClick={onSettings}>Check access or change setup</button></p><label>Run mode<select value={mode} disabled={busy} onChange={event => setMode(event.target.value as typeof mode)}><option value="advisory">Advisory</option><option value="blocking">Blocking gate</option></select></label><p>Advisory reports outcomes. A blocking run returns a failed gate when a required check fails or cannot complete.</p></details>

  </div>
    <aside id="recent-requests" aria-label="Recent requests" ref={historyPanel} tabIndex={-1} onKeyDown={event => {
      if (event.key === 'Escape' && showHistory) {setShowHistory(false); historyToggle.current?.focus()}
    }} className={`task-list ${showHistory ? 'history-open' : ''}`}>
      <h2><History size={17} aria-hidden="true"/>Recent requests</h2>
      {tasks.length === 0 && <p>Saved test requests will appear here.</p>}
      <div className="task-links">{tasks.map(item => <button className={`task-link ${taskId === item.id ? 'selected' : ''}`} aria-current={taskId === item.id ? 'true' : undefined} disabled={busy} key={item.id} onClick={() => {onTask(item.id);setShowHistory(false);workspaceHeading.current?.focus()}}><MessageSquareText size={15} aria-hidden="true"/><span>{item.title}</span><small>{formatDate(item.created_at)}</small></button>)}</div>
    </aside>
  </div>
}
