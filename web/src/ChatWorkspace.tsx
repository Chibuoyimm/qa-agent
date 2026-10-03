import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertCircle, ArrowRight, CheckCircle2, LoaderCircle, MessageSquare, Play, RefreshCw, ShieldCheck, Square } from 'lucide-react'
import AiProposals from './AiProposals'
import { api, formatDate, isActive, providerName, type ChatPage, type ChatRunAction, type ChatTurn, type Project, type Run, type Scenario, type ScenarioInput } from './api'

const requestID = () => crypto.randomUUID().replaceAll('-', '')
const message = (error: unknown) => error instanceof Error ? error.message : 'Could not complete this request.'

function ChatRunCard({ token, runId, disabled, onRerun, onOpen }: {
  token: string; runId: string; disabled: boolean
  onRerun: (id: string) => void; onOpen: (id: string) => void
}) {
  const [run, setRun] = useState<Run | null>(null)
  const [error, setError] = useState('')
  const [cancelling, setCancelling] = useState(false)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      try {
        const value = await api<Run>(token, `/api/runs/${encodeURIComponent(runId)}`, { signal: abort.signal })
        if (abort.signal.aborted) return
        setRun(value); setError('')
        if (isActive(value.status)) timer = setTimeout(() => void load(), 2000)
      } catch (err) { if (!abort.signal.aborted) setError(message(err)) }
    }
    void load()
    return () => { abort.abort(); clearTimeout(timer) }
  }, [token, runId, revision])
  async function cancel() {
    setCancelling(true)
    try {
      const value = await api<Run>(token, `/api/runs/${encodeURIComponent(runId)}/cancel`, { method: 'POST' })
      setRun(value); setRevision(previous => previous + 1)
    } catch (err) { setError(message(err)) }
    finally { setCancelling(false) }
  }
  return <section className="chat-run-card" aria-label="Chat run results">
    {error && <div className="error-banner" role="alert">{error}<button className="text-button" onClick={() => setRevision(value => value + 1)}>Refresh results</button></div>}
    {!run ? <p><LoaderCircle size={15} className="spin" />Loading run…</p> : <>
      <div className="chat-run-head"><strong><span className={`pill pill-${run.status}`}>{isActive(run.status) ? <LoaderCircle size={13} className="spin" /> : run.status === 'passed' ? <CheckCircle2 size={13} /> : <AlertCircle size={13} />}{run.status}</span> {run.scenarios.length} checks · {run.mode}</strong><button className="link-button" onClick={() => onOpen(run.id)}>Full report <ArrowRight size={14} /></button></div>
      <p>{isActive(run.status) ? 'Browser checks are in progress. Results update here automatically.' : run.status === 'passed' ? 'Every selected check passed. Other app behaviour has not been checked by this run.' : run.status === 'cancelled' ? 'Cancelled. Unfinished checks were not tested.' : 'The run did not prove every selected outcome. Review the findings below.'}</p>
      {run.results.map(result => <details className="chat-finding" key={result.scenario_id}><summary><span className={`pill pill-${result.status}`}>{result.status}</span>{run.scenarios.find(item => item.id === result.scenario_id)?.name ?? result.scenario_id}</summary><p>{result.message}</p><small>Expected outcome</small><p>{run.scenarios.find(item => item.id === result.scenario_id)?.expected_outcome}</p></details>)}
      <div className="chat-run-actions">{isActive(run.status) && <button className="button button-outline" disabled={cancelling} onClick={() => void cancel()}><Square size={14} />{cancelling ? 'Cancelling…' : 'Cancel run'}</button>}{!isActive(run.status) && run.results.some(result => result.status === 'failed') && <button className="button button-outline" disabled={disabled} onClick={() => onRerun(run.id)}><RefreshCw size={14} />Rerun failed checks</button>}</div>
    </>}
  </section>
}

export default function ChatWorkspace({ token, project, scenarios, onReview, onOpenRun, onRunQueued, savedScenario }: {
  token: string; project: Project; scenarios: Scenario[]; savedScenario: Scenario | null
  onReview: (input: ScenarioInput) => void; onOpenRun: (id: string) => void; onRunQueued: () => void
}) {
  const [turns, setTurns] = useState<ChatTurn[]>([])
  const [loading, setLoading] = useState(true)
  const [hasOlder, setHasOlder] = useState(false)
  const [error, setError] = useState('')
  const [pendingPrompt, setPendingPrompt] = useState('')
  const [mode, setMode] = useState<'advisory' | 'blocking'>('advisory')
  const [olderLoading, setOlderLoading] = useState(false)
  const [savedHere, setSavedHere] = useState<Scenario[]>([])
  const ownedRequest = useRef<AbortController | null>(null)
  const alive = useRef(true)
  const bottom = useRef<HTMLDivElement>(null)
  const loadedEarlier = useRef(false)
  const approvedCount = scenarios.filter(item => item.approved).length
  const busy = Boolean(pendingPrompt || turns.some(turn => turn.status === 'pending'))
  const refresh = useCallback(async (signal?: AbortSignal) => {
    const page = await api<ChatPage>(token, `/api/projects/${encodeURIComponent(project.id)}/chat`, { signal })
    if (!alive.current || signal?.aborted) return
    setTurns(previous => {
      const latest = new Map(page.turns.map(turn => [turn.id, turn]))
      for (const turn of previous) if (!latest.has(turn.id)) latest.set(turn.id, turn)
      return [...latest.values()].sort((a, b) => a.sequence - b.sequence)
    })
    if (!loadedEarlier.current) setHasOlder(page.has_older)
  }, [token, project.id])
  useEffect(() => {
    alive.current = true
    const abort = new AbortController()
    void refresh(abort.signal).catch(err => { if (!abort.signal.aborted) setError(message(err)) }).finally(() => { if (!abort.signal.aborted) setLoading(false) })
    return () => { alive.current = false; abort.abort(); ownedRequest.current?.abort() }
  }, [refresh])
  useEffect(() => {
    if (!turns.some(turn => turn.status === 'pending')) return
    const abort = new AbortController()
    let polling = false
    const timer = setInterval(() => {
      if (polling) return
      polling = true
      void refresh(abort.signal).catch(err => { if (!abort.signal.aborted) setError(message(err)) }).finally(() => { polling = false })
    }, 2000)
    return () => { abort.abort(); clearInterval(timer) }
  }, [turns, refresh])
  useEffect(() => { if (savedScenario?.project_id === project.id) setSavedHere(previous => previous.some(item => item.id === savedScenario.id) ? previous : [...previous, savedScenario]) }, [savedScenario, project.id])
  useEffect(() => { if (pendingPrompt || turns.length) bottom.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' }) }, [turns.length, pendingPrompt])

  async function send(prompt: string, options: RequestInit) {
    setError(''); setPendingPrompt(prompt)
    try {
      const input = JSON.parse(options.body as string) as Record<string, unknown>
      const turn = await api<ChatTurn>(token, `/api/projects/${encodeURIComponent(project.id)}/chat`, { ...options, body: JSON.stringify({ ...input, request_id: requestID() }) })
      if (alive.current) setTurns(previous => [...previous.filter(item => item.id !== turn.id), turn].sort((a, b) => a.sequence - b.sequence))
    } catch (err) {
      if (alive.current) await refresh().catch(refreshError => setError(message(refreshError)))
      throw err
    } finally { if (alive.current) setPendingPrompt('') }
  }
  async function launch(prompt: string, action: ChatRunAction | 'selected', signal?: AbortSignal, sourceRunId?: string, ids?: string[]) {
    if (busy) throw new Error('Wait for the current chat request to finish.')
    let source = sourceRunId
    if (action === 'rerun_failed' && !source) source = [...turns].reverse().find(turn => turn.run_id)?.run_id
    if (action === 'rerun_failed' && !source) throw new Error('Run checks in this chat first, then choose which failed run to repeat.')
    setError(''); setPendingPrompt(prompt)
    try {
      const turn = await api<ChatTurn>(token, `/api/projects/${encodeURIComponent(project.id)}/chat/runs`, { method: 'POST', signal, body: JSON.stringify({ request_id: requestID(), prompt, action, mode, ...(source ? { source_run_id: source } : {}), ...(ids ? { scenario_ids: ids } : {}) }) })
      if (alive.current) { setTurns(previous => [...previous.filter(item => item.id !== turn.id), turn]); onRunQueued() }
    } catch (err) {
      if (alive.current) await refresh().catch(refreshError => setError(message(refreshError)))
      throw err
    } finally { if (alive.current) setPendingPrompt('') }
  }
  function runFromButton(prompt: string, action: ChatRunAction | 'selected', source?: string, ids?: string[]) {
    const abort = new AbortController(); ownedRequest.current = abort
    void launch(prompt, action, abort.signal, source, ids).catch(err => { if (alive.current && !abort.signal.aborted) setError(message(err)) }).finally(() => { if (ownedRequest.current === abort) ownedRequest.current = null })
  }
  async function older() {
    if (!turns[0]) return
    setOlderLoading(true)
    try {
      const page = await api<ChatPage>(token, `/api/projects/${encodeURIComponent(project.id)}/chat?before=${turns[0].sequence}`)
      if (!alive.current) return
      loadedEarlier.current = true
      setTurns(previous => [...page.turns.filter(turn => !previous.some(item => item.id === turn.id)), ...previous]); setHasOlder(page.has_older)
    } catch (err) { if (alive.current) setError(message(err)) }
    finally { if (alive.current) setOlderLoading(false) }
  }

  return <div className="chat-workspace">
    <div className="page-heading"><div><div className="eyebrow">TEST WITH QA AGENT</div><h1>Chat</h1><p>Describe a feature, refine its checks, and follow the results here.</p></div><MessageSquare size={26} /></div>
    <div className="chat-target"><span><ShieldCheck size={16} />{project.name}</span><small>{project.base_url}</small></div>
    <div className="chat-toolbar"><button className="button button-outline" disabled={busy || loading || approvedCount === 0} onClick={() => runFromButton('Run all approved checks', 'all_approved')}><Play size={15} />Run all approved checks <span>({approvedCount})</span></button><label>Run mode <select aria-label="Chat run mode" value={mode} disabled={busy} onChange={event => setMode(event.target.value as typeof mode)}><option value="advisory">Advisory</option><option value="blocking">Blocking</option></select></label></div>
    {error && <div className="error-banner" role="alert"><AlertCircle size={17} /><span>{error}</span><button className="text-button" onClick={() => void refresh().then(() => setError('')).catch(err => setError(message(err)))}>Refresh chat</button></div>}
    <div className="chat-thread" aria-label="Testing conversation" aria-busy={loading}>
      {loading ? <p><LoaderCircle size={17} className="spin" />Loading conversation…</p> : <>
        {hasOlder && <button className="link-button chat-older" disabled={olderLoading} onClick={() => void older()}>{olderLoading ? 'Loading…' : 'Load earlier messages'}</button>}
        {turns.length === 0 && <div className="chat-welcome"><span className="chat-avatar"><MessageSquare size={22} /></span><h2>What would you like to test?</h2><p>Ask about one feature or a whole user journey. I’ll ask for missing details and propose checks for your review.</p><p>New checks require your approval. “Full check” runs all approved checks in this project and reports their coverage.</p></div>}
        {turns.map(turn => <article className="chat-exchange" key={turn.id}>
          <div className="chat-user-message"><small>You · {formatDate(turn.created_at)}</small><p>{turn.prompt}</p></div>
          <div className="chat-agent-message"><div className="chat-agent-label"><MessageSquare size={16} /><strong>QA Agent</strong>{turn.proposal && <small>{providerName(turn.proposal.provider)} · {turn.proposal.model}</small>}</div>
            {turn.status === 'pending' ? <p><LoaderCircle size={15} className="spin" />Working on your request…</p> : <p>{turn.reply}</p>}
            {turn.proposal && <>
              {turn.proposal.questions.length > 0 && <div className="chat-questions"><strong>Details I need</strong><ol>{turn.proposal.questions.map((question, i) => <li key={i}>{question}</li>)}</ol></div>}
              {turn.proposal.scenarios.map((draft, i) => <section className="chat-draft" key={i}><span className="approval-label is-unapproved">DRAFT · REVIEW REQUIRED</span><h3>{draft.name}</h3><p>{draft.expected_outcome}</p><div><small>{draft.steps.length} browser steps</small><button className="button button-outline" onClick={() => onReview({ ...draft, approved: false })}>Review check <ArrowRight size={14} /></button></div></section>)}
              {turn.proposal.assumptions.length > 0 && <details className="chat-assumptions"><summary>Assumptions to review ({turn.proposal.assumptions.length})</summary><ul>{turn.proposal.assumptions.map((item, i) => <li key={i}>{item}</li>)}</ul></details>}
              {(turn.proposal.repository_snapshots?.length || turn.proposal.discovery_id) ? <details className="chat-assumptions"><summary>Context sources</summary>{turn.proposal.repository_snapshots?.map(source => <p key={source.id}>{source.repository} · {source.role} · {source.commit_sha.slice(0, 12)}</p>)}{turn.proposal.discovery_id && <p>Discovery: {turn.proposal.discovery_id}</p>}</details> : null}
            </>}
            {turn.run_id && <ChatRunCard token={token} runId={turn.run_id} disabled={busy} onOpen={onOpenRun} onRerun={id => runFromButton('Rerun failed checks', 'rerun_failed', id)} />}
          </div>
        </article>)}
        {pendingPrompt && <div className="chat-user-message chat-pending" role="status"><small>You</small><p>{pendingPrompt}</p><span><LoaderCircle size={15} className="spin" />Sending…</span></div>}
      </>}
      <div ref={bottom} />
    </div>
    {savedHere.length > 0 && <div className="chat-saved"><strong>Reviewed checks saved</strong>{savedHere.map(scenario => <div key={scenario.id}><span>{scenario.name} · {scenario.approved ? 'approved' : 'awaiting approval'}</span>{scenario.approved && <button className="link-button" disabled={busy} onClick={() => runFromButton(`Run ${scenario.name}`, 'selected', undefined, [scenario.id])}>Run this check <Play size={13} /></button>}</div>)}</div>}
    <AiProposals token={token} projectId={project.id} onClose={() => {}} onReview={onReview} conversation={{ disabled: busy || loading, send, command: (prompt, action, signal) => launch(prompt, action, signal) }} />
    <p className="chat-storage-note">Conversation history is saved for this project. App context and transient provider keys are not saved in chat. Keep passwords and connection keys out of messages.</p>
  </div>
}
