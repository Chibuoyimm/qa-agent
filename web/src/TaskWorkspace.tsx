import { useEffect, useRef, useState } from 'react'
import { ArrowRight, LoaderCircle, Play, Plus, Sparkles } from 'lucide-react'
import JourneyProgress from './JourneyProgress'
import PlanningRecovery from './PlanningRecovery'
import AiProposals from './AiProposals'
import ScenarioEditor from './ScenarioEditor'
import { api, formatDate, isActive, newRequestID, type ChatPage, type ChatTurn, type Discovery, type Journey, type Project, type Run, type Scenario, type TaskApproval, type TestingTask, validateScenario } from './api'
import { errorText, runCounts, scenarioInput, checkCount, requiresPlanRevision, assertionSummary, stepLabel, shortTitle, assertionKey, targetLabel, journeySummary } from './workspace'

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

function ReviewPlan({ token, project, task, turn, scenarios, onApproved, onRun, onRevise, busy }: { token: string; project: Project; task: TestingTask; turn: ChatTurn; scenarios: Scenario[]; onApproved: () => Promise<void>; onRun: (ids: string[], requestID: string) => Promise<void>; onRevise: (prompt: string) => void; busy: boolean }) {
  const reviewKey = `qa-review:${project.id}:${turn.id}`
  const [drafts, setDrafts] = useState(() => {
    try { const saved = JSON.parse(sessionStorage.getItem(reviewKey) || 'null'); const items = Array.isArray(saved) ? saved : saved?.drafts; if (Array.isArray(items) && items.length === turn.proposal?.scenarios.length) return items.map(validateScenario) } catch { /* Restore the original proposal if a draft is unavailable. */ }
    return (turn.proposal?.scenarios ?? []).map(scenarioInput)
  })
  const [selected, setSelected] = useState<number[]>(() => {
    try { const saved = JSON.parse(sessionStorage.getItem(reviewKey) || 'null'); if (Array.isArray(saved?.selected)) return [...new Set<number>(saved.selected.filter((index: unknown): index is number => typeof index === 'number' && Number.isInteger(index) && index >= 0 && index < drafts.length))] } catch { /* Select the proposed plan by default. */ }
    return drafts.map((_, i) => i)
  })
  const [unresolved, setUnresolved] = useState<number[]>(() => {
    try { const value = JSON.parse(sessionStorage.getItem(reviewKey) || 'null'); return Array.isArray(value?.unresolved) ? value.unresolved : [] } catch { return [] }
  })
  const [reviewed, setReviewed] = useState(false)
  const needsRevision = selected.some(i => unresolved.includes(i) || requiresPlanRevision(turn.proposal!.scenarios[i], drafts[i]))
  const edited = selected.some(i => JSON.stringify(scenarioInput(turn.proposal!.scenarios[i])) !== JSON.stringify(drafts[i]))
  useEffect(() => setReviewed(false), [drafts, selected])
  const [editing, setEditing] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [approvedHere, setApprovedHere] = useState<TaskApproval | null>(null)
  const retryID = useRef(newRequestID())
  const completeJourney = drafts.length === 1 && journeySummary(drafts[0].steps).phases.length > 0
  const approval = task.approvals.find(item => item.turn_id === turn.id) ?? approvedHere
  useEffect(() => {
    try { if (approval) sessionStorage.removeItem(reviewKey); else sessionStorage.setItem(reviewKey, JSON.stringify({ drafts, selected, unresolved })) } catch { /* Approval remains available if tab storage is full. */ }
  }, [approval, drafts, selected, unresolved, reviewKey])
  async function approveAndRun() {
    if (!approval && (needsRevision || (edited && !reviewed))) { setError('Review the updated assertions before approval. A changed requirement needs an updated plan.'); return }
    setSaving(true); setError('')
    try {
      const approved = approval ?? await api<TaskApproval>(token, `/api/projects/${project.id}/tasks/${task.id}/approve`, { method: 'POST', body: JSON.stringify({ turn_id: turn.id, drafts: selected.map(index => ({ index, ...drafts[index], approved: true })) }) })
      setApprovedHere(approved)
      await onApproved()
      await onRun(approved.scenario_ids, retryID.current)
      retryID.current = newRequestID()
    } catch (err) { setError(errorText(err)) } finally { setSaving(false) }
  }
  if (!drafts.length) return null
  if (approval) return <section className="review-plan"><div className="eyebrow">APPROVED PLAN · {checkCount(approval.scenario_ids.length).toUpperCase()}</div>{approval.scenario_ids.map(id => { const check = scenarios.find(item => item.id === id); return <div className="plan-check" key={id}><strong>{check?.name ?? 'Approved check'}</strong><p>{check?.expected_outcome ?? 'Loading saved expectation…'}</p><small>{check?.steps.length ?? '—'} browser steps · Approved</small></div> })}{error && <p role="alert">{error}</p>}<button className="button button-primary" disabled={busy || saving} onClick={() => void approveAndRun()}><Play size={15} />{saving ? 'Starting…' : completeJourney ? 'Run journey again' : `Run ${checkCount(approval.scenario_ids.length)} again`}</button></section>
  return <section className="review-plan"><div className="flow-row"><div><div className="eyebrow">REVIEW THE PLAN</div><h3>{checkCount(selected.length)} selected</h3></div></div><p>Review the expected behavior and browser steps. The runner keeps these expectations fixed.</p>
    {drafts.map((draft, index) => <article className="plan-check" key={index}><label className="inline-check"><input type="checkbox" checked={selected.includes(index)} disabled={Boolean(approval) || saving} onChange={event => setSelected(previous => event.target.checked ? [...previous, index].sort((a, b) => a - b) : previous.filter(i => i !== index))} /><strong>{index + 1}. {draft.name}</strong></label>{journeySummary(draft.steps).phases.length>0&&<div className="journey-plan-summary"><strong>Complete journey · {journeySummary(draft.steps).phases.length} stages{journeySummary(draft.steps).fixture ? ' + preparation & cleanup' : ''} · {Math.max(1,journeySummary(draft.steps).actors.length)} participants</strong><p>{journeySummary(draft.steps).phases.join(' → ')}</p><p>{journeySummary(draft.steps).fixture?'Fresh-data preparation and cleanup included in every run.':'Uses existing app data with fresh browser sessions.'} Later stages stop when a required step fails.</p></div>}<label className="field-label" htmlFor={`outcome-${turn.id}-${index}`}>Expected outcome</label><textarea id={`outcome-${turn.id}-${index}`} rows={2} maxLength={4000} value={draft.expected_outcome} disabled={Boolean(approval) || saving} onChange={event => { setUnresolved(previous => [...new Set([...previous.filter(i=>i!==index), ...(event.target.value !== turn.proposal!.scenarios[index].expected_outcome ? [index] : [])])]); setDrafts(previous => previous.map((item, i) => i === index ? { ...item, expected_outcome: event.target.value } : item)) }} /><div className="assertion-editor"><strong>What the browser will check</strong>{draft.steps.map((step, stepIndex) => step.action === 'assert_text' ? <label key={stepIndex}>Exact text for {step.test_id || targetLabel(step)}<input value={step.value ?? ''} disabled={saving} onChange={event => {setUnresolved(previous=>previous.filter(i=>i!==index)); setDrafts(previous => previous.map((item, i) => { if (i !== index) return item; const steps = item.steps.map((value, j) => j === stepIndex ? { ...value, value: event.target.value } : value); return { ...item, steps, expected_outcome: assertionKey(steps) === assertionKey(turn.proposal!.scenarios[index].steps) ? turn.proposal!.scenarios[index].expected_outcome : assertionSummary(steps) } })) }} /></label> : step.action.startsWith('assert_') ? <p key={stepIndex}>{stepLabel(step)}</p> : null)}</div>
    {(unresolved.includes(index) || requiresPlanRevision(turn.proposal!.scenarios[index], draft)) && <div className="plan-revision" role="status"><strong>Requirement changed — update the plan before approval.</strong><p>Edit the checked values above, or ask the agent to revise this check.</p><button className="link-button" disabled={busy || saving} onClick={() => onRevise(`Revise the check “${draft.name}”. The required outcome is now: ${draft.expected_outcome}. Update the executable assertion steps to prove this requirement. Return a new unapproved plan for review; do not run it.`)}>Revise plan in chat</button></div>}
    <details><summary>{draft.steps.length} browser steps</summary><ol>{draft.steps.map((step, i) => <li key={i}>{stepLabel(step)}</li>)}</ol>{!approval && <button className="link-button" disabled={saving} onClick={() => setEditing(index)}>Edit this check</button>}</details></article>)}
    {edited && !needsRevision && <label className="inline-check revised-confirmation"><input type="checkbox" checked={reviewed} onChange={event => setReviewed(event.target.checked)} />I reviewed the revised outcomes and the exact assertions shown above.</label>}
    {error && <div className="error-banner" role="alert">{error}</div>}<button className="button button-primary" disabled={busy || saving || (!approval && (selected.length === 0 || needsRevision || (edited && !reviewed)))} onClick={() => void approveAndRun()}>{saving ? <LoaderCircle size={16} className="spin" /> : <Play size={16} />}{saving ? 'Saving and starting…' : completeJourney ? 'Approve & run journey' : `Approve & run ${checkCount(selected.length)}`}</button>
    {editing !== null && <ScenarioEditor token={token} projectId={project.id} initial={drafts[editing]} source="proposal" onClose={() => setEditing(null)} onSaved={() => {}} onDraft={draft => {if (assertionKey(draft.steps) !== assertionKey(drafts[editing].steps)) setUnresolved(previous => previous.filter(i=>i!==editing)); setDrafts(previous => previous.map((item, i) => i === editing ? draft : item))}} />}
  </section>
}

export default function TaskWorkspace({ token, project, taskId, journeys, scenarios, onTask, onChanged, onOpenRun, initialRunContext, onContextUsed, configRevision, initialJourney = '', defaultSetup = '', onAIReady, onSettings, onAISettings }: { token: string; project: Project; taskId: string; journeys: Journey[]; scenarios: Scenario[]; onTask: (id: string) => void; onChanged: () => void; onOpenRun: (id: string) => void; initialRunContext: string; onContextUsed: () => void; configRevision: number; initialJourney?: string; defaultSetup?: string; onAIReady: (ready: boolean) => void; onSettings: () => void; onAISettings: () => void }) {
  const [tasks, setTasks] = useState<TestingTask[]>([])
  const [task, setTask] = useState<TestingTask | null>(null)
  const [turns, setTurns] = useState<ChatTurn[]>([])
  const [hasOlder, setHasOlder] = useState(false)
  const [journey, setJourney] = useState(initialJourney)
  const [prefill, setPrefill] = useState<{text: string; id: number} | undefined>()
  const selectedJourneyName = journeys.find(j => j.id === initialJourney)?.name
  useEffect(() => { setJourney(initialJourney); if (initialJourney && !taskId && selectedJourneyName) setPrefill({text: `Test the ${selectedJourneyName} journey.`, id: Date.now()}) }, [initialJourney, selectedJourneyName, taskId])
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
    try { const draft = sessionStorage.getItem(`qa-task-draft:${project.id}:new`); if (draft) sessionStorage.setItem(`qa-task-draft:${project.id}:${value.id}`, draft); sessionStorage.removeItem(`qa-task-draft:${project.id}:new`) } catch { /* The task is still saved on the server. */ }
    currentTask.current = value.id; setTask(value); onTask(value.id); setRevision(value => value + 1); onChanged()
    taskRequest.current = newRequestID()
    return value.id
  }
  async function send(prompt: string, options: RequestInit) {
    setProgress('Saving your request…'); setError('')
    let id = ''
    try {
      id = await ensureTask(prompt, options.signal ?? undefined)
      const input = JSON.parse(String(options.body)) as Record<string, unknown>
      if (explore && !input.fixture_id && !input.discovery_id) {
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
      setProgress('Reading the context and preparing checks…')
      await api<ChatTurn>(token, `${root}/tasks/${id}/chat`, { ...options, body: JSON.stringify({ ...input, request_id: newRequestID(), ...(initialRunContext ? { run_context_id: initialRunContext } : {}) }) })
      onContextUsed()
    } finally { setProgress(''); if (id && alive.current) await refresh(id).catch(err => setError(errorText(err))) }
  }
  async function launch(prompt: string, action: string, signal?: AbortSignal, ids?: string[], requestID = newRequestID()) {
    setProgress('Queuing approved checks…'); setError('')
    let id = ''
    try {
      id = await ensureTask(prompt, signal)
      const source = [...turns].reverse().find(turn => turn.run_id)?.run_id
      if (action === 'rerun_failed' && !source) throw new Error('Open an earlier run in Results to recheck its approved checks.')
      const turn = await api<ChatTurn>(token, `${root}/tasks/${id}/runs`, { method: 'POST', signal, body: JSON.stringify({ request_id: requestID, prompt, action, mode, ...(ids ? { scenario_ids: ids } : {}), ...(action === 'rerun_failed' ? { source_run_id: source } : {}) }) })
      if (currentTask.current === id) setTurns(previous => [...previous.filter(item => item.id !== turn.id), turn])
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
  return <div className="testing-layout"><aside className="task-list"><button className="button button-primary" disabled={busy} onClick={() => { onTask(''); taskRequest.current = newRequestID() }}><Plus size={16} />New test request</button><h3>Recent requests</h3>{tasks.length === 0 && <p>Your testing conversations will appear here.</p>}{tasks.map(item => <button className={`task-link ${taskId === item.id ? 'selected' : ''}`} disabled={busy} key={item.id} onClick={() => onTask(item.id)}><span>{item.title}</span><small>{formatDate(item.created_at)}</small></button>)}</aside><div className="testing-main"><div className="page-heading"><div><div className="eyebrow">TEST WORKSPACE</div><h1>{journeys.find(item => item.id === task?.journey_id)?.name || task?.title || 'What would you like to test?'}</h1><p>{project.name} · {project.base_url}</p></div></div>
    {!taskId && <><div className="flow-card"><h2>Describe the outcome. We’ll find the steps.</h2><p>Ask for a feature or user journey to be tested. Review the plan, run the checks, and follow the evidence here.</p><div className="flow-stages"><span>1 Understand</span><span>2 Review plan</span><span>3 Run checks</span><span>4 Fix & recheck</span></div></div><details className="flow-card journey-options"><summary>Journey · {journeys.find(item => item.id === journey)?.name || journeyName || 'New journey'}</summary><div className="task-options"><label>Journey<select value={journey} onChange={event => setJourney(event.target.value)}><option value="">New journey</option>{journeys.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>{!journey && <label>Journey name <span className="muted">(optional)</span><input maxLength={200} placeholder="e.g. Checkout" value={journeyName} onChange={event => setJourneyName(event.target.value)} /></label>}</div></details></>}
    {error && <div className="error-banner" role="alert">{error}</div>}{loading && <p role="status">Loading saved conversation…</p>}
    <div className="chat-thread">{hasOlder && <button className="link-button" onClick={() => void older()}>Load earlier messages</button>}{turns.map(turn => <article className="chat-exchange" key={turn.id}><div className="chat-user-message"><small>You · {formatDate(turn.created_at)}</small><p>{turn.prompt}</p></div><div className="chat-agent-message"><div className="chat-agent-label"><Sparkles size={17} /><strong>QA Agent</strong>{turn.proposal && <small>{turn.proposal.model}</small>}</div>{turn.status === 'error' ? <PlanningRecovery turn={turn} disabled={busy} onRetry={text => setPrefill({text, id: Date.now()})} onSettings={onAISettings} /> : <p>{turn.status === 'pending' ? 'Preparing your test plan…' : turn.reply}</p>}{turn.proposal && <>{turn.proposal.questions.length > 0 && <div className="chat-questions"><strong>A few details before we test</strong><ol>{turn.proposal.questions.map((question, i) => <li key={i}>{question}</li>)}</ol></div>}{turn.proposal.assumptions.length > 0 && <details className="chat-assumptions"><summary>Assumptions to review ({turn.proposal.assumptions.length})</summary><ul>{turn.proposal.assumptions.map((item, i) => <li key={i}>{item}</li>)}</ul></details>}{task && <ReviewPlan token={token} project={project} task={task} turn={turn} scenarios={scenarios} busy={busy} onApproved={() => refresh(task.id)} onRevise={text => setPrefill({text, id: Date.now()})} onRun={(ids, requestID) => launch('Run the reviewed plan', 'selected', undefined, ids, requestID)} />}{(turn.proposal.discovery_id || turn.proposal.repository_snapshots?.length) ? <details className="chat-assumptions"><summary>Context used for this plan</summary>{turn.proposal.discovery_id && <p>Observed app pages from discovery {turn.proposal.discovery_id.slice(0, 8)}</p>}{turn.proposal.repository_snapshots?.map(source => <p key={source.id}>{source.repository} · {source.role} · {source.commit_sha.slice(0, 12)}</p>)}</details> : null}</>}{turn.run_id && <RunCard token={token} id={turn.run_id} onOpen={onOpenRun} />}</div></article>)}</div>
    {progress && <div className="flow-progress" role="status"><LoaderCircle className="spin" size={18} />{progress}</div>}

    {initialRunContext && <div className="flow-card">The next message will include this run’s approved checks and results.<button className="link-button" onClick={onContextUsed}>Remove run context</button></div>}
    <AiProposals targetOrigin={project.base_url} token={token} projectId={project.id} configRevision={configRevision} onReady={onAIReady} onClose={() => {}} onReview={() => {}} conversation={{ prefill, disabled: busy || loading, draftKey: `qa-task-draft:${project.id}:${taskId || 'new'}`, initialPrompt: !loading && task && turns.length === 0 ? task.request : undefined, taskContext: true, send, command: (prompt, action, signal) => launch(prompt, action, signal) }} />
    <details className="flow-card exploration-options"><summary>App access & exploration</summary><label className="inline-check"><input type="checkbox" checked={explore} disabled={busy} onChange={event => setExplore(event.target.checked)} />Explore up to 3 app pages before planning</label><p>Discovery follows same-origin links. Its observations help find controls; your requirements establish the expected outcome.</p><label>Sign-in or access setup<select value={setup} disabled={busy} onChange={event => { setSetup(event.target.value) }}><option value="">No setup (public app)</option>{scenarios.filter(s => s.approved && !journeySummary(s.steps).phases.length && !journeySummary(s.steps).fixture).map(s => <option key={s.id} value={s.id}>{s.name}</option>)}</select></label><p>Your verified default is reused for new requests. <button className="link-button" onClick={onSettings}>Check access or change setup</button></p><label>Run mode<select value={mode} disabled={busy} onChange={event => setMode(event.target.value as typeof mode)}><option value="advisory">Advisory</option><option value="blocking">Blocking gate</option></select></label><p>Advisory reports outcomes. A blocking run returns a failed gate when a required check fails or cannot complete.</p></details>
    <div className="flow-row workspace-footer"><p>New checks run only after approval. A full check runs the app’s existing approved checks.</p><button className="link-button" disabled={busy || !scenarios.some(s => s.approved)} onClick={() => void launch('Run all approved checks', 'all_approved').catch(err => setError(errorText(err)))}>Run all approved checks <Play size={14} /></button></div>
  </div></div>
}
