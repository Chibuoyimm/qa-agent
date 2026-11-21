import { useEffect, useRef, useState } from 'react'
import { Check, ChevronDown, LoaderCircle, Pencil, Play, SlidersHorizontal } from 'lucide-react'
import ScenarioEditor from './ScenarioEditor'
import { api, newRequestID, validateScenario, type Project, type Run, type Scenario, type ScenarioInput, type TestingTask, type TaskApproval, type ChatTurn } from './api'
import { assertionKey, assertionSummary, checkCount, errorText, journeyStages, journeySummary, requiresPlanRevision, scenarioInput, stepLabel, targetLabel } from './workspace'

export type PlanRunSettings = { target: string; mode: Run['mode'] }

type Props = {
  token: string
  project: Project
  task: TestingTask
  turn: ChatTurn
  scenarios: Scenario[]
  busy: boolean
  defaultMode: Run['mode']
  onApproved: () => Promise<void>
  onRun: (ids: string[], requestID: string, settings: PlanRunSettings) => Promise<void>
  onRevise: (prompt: string) => void
}

export default function ReviewPlan({ token, project, task, turn, scenarios, onApproved, onRun, onRevise, busy, defaultMode }: Props) {
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
  const [expanded, setExpanded] = useState<number | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [targets, setTargets] = useState<string[]>([])
  const [targetsError, setTargetsError] = useState('')
  const settingsKey = `qa-plan-run:${project.id}:${turn.id}`
  const [override, setOverride] = useState<PlanRunSettings | null>(() => {
    try {
      const saved = JSON.parse(sessionStorage.getItem(settingsKey) || 'null')
      if (typeof saved?.target === 'string' && ['advisory', 'blocking'].includes(saved.mode)) return saved
    } catch { /* Use the project defaults when storage is unavailable. */ }
    return null
  })
  const settings = override ?? { target: project.base_url, mode: defaultMode }
  useEffect(() => {
    if (!settingsOpen) return
    const abort = new AbortController()
    setTargetsError('')
    void api<string[]>(token, '/api/run-targets', { signal: abort.signal }).then(setTargets).catch(err => {
      if (!abort.signal.aborted) setTargetsError(errorText(err))
    })
    return () => abort.abort()
  }, [settingsOpen, token])
  function changeSettings(next: PlanRunSettings) {
    setOverride(next)
    retryID.current = newRequestID()
    try { sessionStorage.setItem(settingsKey, JSON.stringify(next)) } catch { /* Settings still apply to this run. */ }
  }
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
      const approved = approval ?? await api<TaskApproval>(token, `/api/projects/${project.id}/tasks/${task.id}/approve`, { method: 'POST', body: JSON.stringify({ turn_id: turn.id, base_url: settings.target, drafts: selected.map(index => ({ index, ...drafts[index], approved: true })) }) })
      setApprovedHere(approved)
      await onApproved()
      await onRun(approved.scenario_ids, retryID.current, settings)
      retryID.current = newRequestID()
    } catch (err) { setError(errorText(err)) } finally { setSaving(false) }
  }
  function editOutcome(index: number, expected_outcome: string) {
    const changed = expected_outcome !== turn.proposal!.scenarios[index].expected_outcome
    setUnresolved(previous => [...previous.filter(i => i !== index), ...(changed ? [index] : [])])
    setDrafts(previous => previous.map((item, i) => i === index ? { ...item, expected_outcome } : item))
  }

  function editAssertion(index: number, stepIndex: number, value: string) {
    setUnresolved(previous => previous.filter(i => i !== index))
    setDrafts(previous => previous.map((item, i) => {
      if (i !== index) return item
      const steps = item.steps.map((step, j) => j === stepIndex ? { ...step, value } : step)
      const original = turn.proposal!.scenarios[index]
      const expected_outcome = assertionKey(steps) === assertionKey(original.steps) ? original.expected_outcome : assertionSummary(steps)
      return { ...item, steps, expected_outcome }
    }))
  }

  function renderAssertion(draft: ScenarioInput, index: number, stepIndex: number) {
    const step = draft.steps[stepIndex]
    if (step.action === 'assert_text') return <label key={stepIndex}>Exact text for {step.test_id || targetLabel(step)}
      <input value={step.value ?? ''} disabled={saving} onChange={event => editAssertion(index, stepIndex, event.target.value)} />
    </label>
    return step.action.startsWith('assert_') ? <p key={stepIndex}>{stepLabel(step)}</p> : null
  }
  if (!drafts.length) return null
  const count = approval ? approval.scenario_ids.length : selected.length
  const runLabel = completeJourney ? 'journey' : checkCount(count)
  const actionLabel = approval ? `Run ${runLabel} again` : `Approve & run ${runLabel}`
  const locked = busy || saving
  const blocked = !approval && (!count || needsRevision || (edited && !reviewed))
  const policyLabel = settings.mode === 'blocking' ? 'Block release on failure' : 'Report findings'

  return <section className="review-plan" aria-label={approval ? 'Approved plan' : 'Review the plan'}>
    <header className="plan-decision">
      <div><div className="eyebrow">{approval ? 'APPROVED PLAN' : 'REVIEW THE PLAN'}</div>
        <h3 aria-live="polite">{checkCount(count)} {approval ? 'approved' : 'selected'}</h3>
      </div>
      <button type="button" className="button button-primary" disabled={locked || blocked} onClick={() => void approveAndRun()}>
        {saving ? <LoaderCircle className="spin" size={15}/> : <Play size={15}/>}
        {saving ? 'Starting…' : actionLabel}
      </button>
    </header>
    <div className="plan-context">
      <span>Target <bdi>{settings.target}</bdi></span><span className="plan-policy">{policyLabel}</span>
      <button type="button" className="link-button" aria-label={settingsOpen ? 'Done changing run settings' : 'Change run settings'} aria-expanded={settingsOpen} aria-controls={`settings-${turn.id}`} onClick={() => setSettingsOpen(!settingsOpen)}>
        <SlidersHorizontal size={13} aria-hidden="true"/>{settingsOpen ? 'Done' : 'Change'}
      </button>
    </div>
    {settingsOpen && <div className="plan-settings" id={`settings-${turn.id}`}>
      <label>Deployment target<select value={settings.target} disabled={locked || !targets.length} onChange={event => changeSettings({...settings, target: event.target.value})}>
        {!targets.includes(settings.target) && <option value={settings.target}>{settings.target}</option>}
        {targets.map(target => <option key={target} value={target}>{target}</option>)}
      </select></label>
      <label>Release policy<select value={settings.mode} disabled={locked} onChange={event => changeSettings({...settings, mode: event.target.value as Run['mode']})}>
        <option value="advisory">Report findings</option><option value="blocking">Block release on failure</option>
      </select></label>
      {targetsError && <p role="alert">{targetsError}</p>}
    </div>}
    {error && <div className="error-banner" role="alert">{error}</div>}
    {edited && !approval && !needsRevision && <label className="revised-confirmation">
      <input type="checkbox" checked={reviewed} disabled={locked} onChange={event => setReviewed(event.target.checked)} />
      I reviewed the updated outcomes and browser assertions.
    </label>}
    <div className="plan-checks">
      {approval ? approval.scenario_ids.map(id => {
        const check = scenarios.find(item => item.id === id)
        return <article key={id} className="plan-check">
          <div className="plan-row approved-row"><Check size={16} aria-hidden="true"/><div>
            <strong>{check?.name ?? 'Approved check'}</strong><p>{check?.expected_outcome ?? 'Loading saved expectation…'}</p>
            {check && <details><summary>{check.steps.length} browser steps</summary><ol>{check.steps.map((step,i) => <li key={i}>{stepLabel(step)}</li>)}</ol></details>}
          </div></div>
        </article>
      }) : drafts.map((draft, index) => {
        const open = expanded === index
        const included = selected.includes(index)
        const changedRequirement = unresolved.includes(index) || requiresPlanRevision(turn.proposal!.scenarios[index], draft)
        const journey = journeySummary(draft.steps)
        const editorId = `edit-${turn.id}-${index}`
        return <article key={index} className="plan-check" data-selected={included}>
          <div className="plan-row">
            <label className="plan-selection">
              <input type="checkbox" checked={included} disabled={locked} onChange={event => {
                setSelected(event.target.checked ? [...selected, index].sort((a,b) => a-b) : selected.filter(i => i !== index))
              }}/>
              <strong>{index + 1}. {draft.name}</strong>
            </label>
            <button type="button" className="plan-edit link-button" aria-label={`${open ? 'Close editor for' : 'Edit'} ${draft.name}`} aria-expanded={open} aria-controls={editorId} onClick={() => setExpanded(open ? null : index)}>
              {open ? <ChevronDown size={14} aria-hidden="true"/> : <Pencil size={13} aria-hidden="true"/>}{open ? 'Done' : 'Edit'}
            </button>
            {!open && <p className="plan-outcome" data-long={draft.expected_outcome.length > 240}>{draft.expected_outcome || 'Enter an expected outcome'}</p>}
            {journey.phases.length > 0 && <small className="plan-stage-count">{journey.phases.length} stages · {journey.assertions} assertions{journey.fixture && ' · Fresh data and cleanup'}</small>}
          </div>
          {changedRequirement && !open && <p className="plan-revision" role="status">Edit this check to review its changed requirement.</p>}
          {open && <div className="plan-editor" id={editorId}>
            <label className="field-label" htmlFor={`outcome-${turn.id}-${index}`}>Expected outcome</label>
            <textarea id={`outcome-${turn.id}-${index}`} rows={2} maxLength={4000} value={draft.expected_outcome} disabled={locked} onChange={event => editOutcome(index, event.target.value)} />
            {journey.phases.length > 0 ? <>
              <p>{journey.actors.length > 0 && <>Participants · {journey.actors.map(actor => actor.replaceAll('_',' ')).join(', ')}. </>}
                {journey.fixture ? 'Each run prepares and cleans up its test data.' : 'Each run starts fresh browser sessions with existing app data.'} Later stages stop if a required step fails.</p>
              <details className="assertion-editor"><summary>Review {journey.assertions} assertions by stage</summary>
                {journeyStages(draft.steps, undefined, false).map((stage, i) => <section key={i}><h4>{stage.name}</h4>{stage.indexes.map(stepIndex => renderAssertion(draft,index,stepIndex))}</section>)}
              </details>
            </> : <div className="assertion-editor"><strong>What the browser will check</strong>{draft.steps.map((_, stepIndex) => renderAssertion(draft,index,stepIndex))}</div>}
            {changedRequirement && <div className="plan-revision" role="status"><strong>Update the plan before approval</strong>
              <p>Edit the checked values or ask the agent to revise this check.</p>
              <button className="link-button" disabled={locked} onClick={() => onRevise(`Revise the check “${draft.name}”. The required outcome is now: ${draft.expected_outcome}. Update the executable assertion steps to prove this requirement. Return a new unapproved plan for review; do not run it.`)}>Revise plan in chat</button>
            </div>}
            <details open={draft.steps.length <= 8}><summary>{draft.steps.length} browser steps</summary><ol>{draft.steps.map((step, i) => <li key={i}>{stepLabel(step)}</li>)}</ol>
              <button type="button" className="link-button" disabled={locked} onClick={() => setEditing(index)}>Edit browser steps</button>
            </details>
          </div>}
        </article>
      })}
    </div>
    {editing !== null && <ScenarioEditor token={token} projectId={project.id} initial={drafts[editing]} source="proposal" onClose={() => setEditing(null)} onSaved={() => {}} onDraft={draft => {
      if (assertionKey(draft.steps) !== assertionKey(drafts[editing].steps)) setUnresolved(previous => previous.filter(i => i !== editing))
      setDrafts(previous => previous.map((item, i) => i === editing ? draft : item))
    }}/>}
  </section>
}
