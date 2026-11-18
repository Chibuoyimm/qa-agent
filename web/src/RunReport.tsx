import JourneyProgress from './JourneyProgress'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ArrowLeft, ArrowRight, RefreshCw, Square, Image, Video } from 'lucide-react'
import { api, formatDate, isActive, newRequestID, type Artifact, type FindingReview, type Run, type Scenario, type ScenarioResult } from './api'
import { errorText, runCounts, compareRuns, completionLabel, checkCount, stepLabel } from './workspace'

function Evidence({ token, run, scenario, artifact, index }: { token: string; run: Run; scenario: Scenario; artifact: Artifact; index: number }) {
  const [url, setURL] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [opened, setOpened] = useState(false)
  useEffect(() => {
    if (!opened) return
    const abort = new AbortController()
    let objectURL = ''
    setLoading(true); setError('')
    void fetch(`/api/runs/${run.id}/artifacts/${scenario.id}/${index}`, { headers: { Authorization: `Bearer ${token}` }, signal: abort.signal }).then(async response => {
      if (!response.ok) { const body = await response.json().catch(() => null); throw new Error(body?.error || 'Evidence could not be opened.') }
      const blob = await response.blob()
      if (!abort.signal.aborted) { objectURL = URL.createObjectURL(blob); setURL(objectURL) }
    }).catch(err => { if (!abort.signal.aborted) setError(errorText(err)) }).finally(() => { if (!abort.signal.aborted) setLoading(false) })
    return () => { abort.abort(); if (objectURL) URL.revokeObjectURL(objectURL) }
  }, [token, run.id, scenario.id, index, opened])
  return <div className="evidence"><button className="button button-outline" onClick={() => setOpened(previous => !previous)}>{artifact.kind === 'video' ? <Video size={15}/> : <Image size={15}/>} {opened ? 'Hide' : 'Open'} {artifact.actor ? artifact.actor.replaceAll('_',' ')+' ' : ''}{artifact.kind === 'video' ? 'browser recording' : 'final screenshot'}</button>{opened && <>{loading && <p>Loading evidence…</p>}{error && <p role="alert">{error}</p>}{url && (artifact.kind === 'video' ? <video controls preload="metadata" src={url} aria-label={`Browser recording for ${scenario.name}`} /> : <img src={url} alt={`Final browser view for ${scenario.name}`} />)}</>}</div>
}

function Finding({ token, run, result, initial }: { token: string; run: Run; result: ScenarioResult; initial?: FindingReview }) {
  const [status, setStatus] = useState(initial?.status ?? 'Needs review')
  const [owner, setOwner] = useState(initial?.owner ?? '')
  const [reason, setReason] = useState(initial?.reason ?? '')
  const [notice, setNotice] = useState('')
  const [saving, setSaving] = useState(false)
  async function save(event: FormEvent) {
    event.preventDefault(); setSaving(true); setNotice('')
    try { await api(token, `/api/runs/${run.id}/reviews`, { method: 'POST', body: JSON.stringify({ scenario_id: result.scenario_id, status, owner, reason }) }); setNotice('Review saved. The recorded result and approved expectation are unchanged.') } catch (err) { setNotice(errorText(err)) } finally { setSaving(false) }
  }
  return <form className="finding-review" onSubmit={event => void save(event)}><h4>Review this finding</h4><div className="task-options"><label>Decision<select value={status} onChange={event => setStatus(event.target.value)}>{['Needs review', 'Confirmed issue', 'Environment issue', 'Works as intended'].map(item => <option key={item}>{item}</option>)}</select></label><label>Owner (optional)<input value={owner} maxLength={200} onChange={event => setOwner(event.target.value)} placeholder="Name or team" /></label></div><label>Review note<textarea value={reason} rows={2} required={status === 'Works as intended'} maxLength={2000} onChange={event => setReason(event.target.value)} /></label><button className="button button-outline" disabled={saving}>{saving ? 'Saving…' : 'Save review'}</button>{notice && <p role="status">{notice}</p>}</form>
}

function ResultEvidence({token,run,scenario,result,participant}:{token:string;run:Run;scenario:Scenario;result:ScenarioResult;participant?:string}) {
  const artifacts = result.artifacts.map((artifact,index) => ({artifact,index}))
  if (participant) artifacts.sort((a,b) => Number(b.artifact.actor === participant) - Number(a.artifact.actor === participant))
  return <div className="evidence-list">{artifacts.length ? artifacts.map(({artifact,index}) =>
    <Evidence key={index} token={token} run={run} scenario={scenario} artifact={artifact} index={index}/>
  ) : <p className="muted">No browser evidence was saved for this check.</p>}</div>
}

function FailureSummary({scenario,result,run,token}:{scenario:Scenario;result:ScenarioResult;run:Run;token:string}) {
  const records = result.step_results ?? []
  const failure = records.find(record => ['failed','blocked','error'].includes(record.status))
  const failedStep = failure && scenario.steps[failure.index]
  const priorSteps = failure ? scenario.steps.slice(0,failure.index+1) : []
  const fixtureStep = failedStep?.action === 'prepare_fixture' || failedStep?.action === 'cleanup_fixture'
  const stage = fixtureStep ? (failedStep.action === 'prepare_fixture' ? 'Prepare fresh test data' : 'Clean up test data') : priorSteps.filter(step => step.action === 'phase').at(-1)?.value
  const actor = fixtureStep ? undefined : priorSteps.filter(step => step.action === 'use_actor').at(-1)?.value
  const skipped = records.filter(record => record.status === 'not_run').length
  const cleanup = scenario.steps.findIndex(step => step.action === 'cleanup_fixture')
  const cancelled = run.status === 'cancelled' && result.status !== 'failed'
  return <><div className="eyebrow">{cancelled ? 'RUN CANCELLED' : result.status === 'failed' ? 'FIRST FAILURE' : 'CHECK INCONCLUSIVE'}</div>
    <h2>{stage || scenario.name}</h2>
    {actor && <p>Participant · {actor.replaceAll('_',' ')}</p>}
    <p className="finding-expectation"><strong>{cancelled ? 'The run stopped before completion.' : failedStep ? stepLabel(failedStep) : scenario.expected_outcome}</strong></p>
    {failure?.observed !== undefined && <p className="actual-value">Observed: “{failure.observed}”</p>}
    {!cancelled && (failure?.message || result.message) && <p className="finding-message">{failure?.message || result.message}</p>}
    <p>{records.length ? `${skipped} subsequent ${skipped === 1 ? 'step' : 'steps'} skipped` : 'No individual step records are available.'}{cleanup >= 0 && <> · Test-data cleanup {records.find(r=>r.index===cleanup)?.status.replace('_',' ') || 'unconfirmed'}</>}</p>
    <ResultEvidence token={token} run={run} scenario={scenario} result={result} participant={actor}/>
  </>
}

export default function RunReport({ token, id, onBack, onOpenRun, onInvestigate, onChanged, snapshotHref, title }: { token: string; id: string; onBack: () => void; onOpenRun: (id: string) => void; onInvestigate: (run: Run) => void; onChanged: () => void; title?: string; snapshotHref: (id: string) => string }) {
  const [run, setRun] = useState<Run | null>(null)
  const [original, setOriginal] = useState<Run | null>(null)
  const [originalReviews, setOriginalReviews] = useState<FindingReview[]>([])
  const [comparisonError, setComparisonError] = useState('')
  const [reviews, setReviews] = useState<FindingReview[]>([])
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [rechecking, setRechecking] = useState(false)
  const [target, setTarget] = useState('')
  const [showRecheck, setShowRecheck] = useState(false)
  const requestID = useRef(newRequestID())
  const recheckButton = useRef<HTMLButtonElement>(null)
  const reportHeading = useRef<HTMLHeadingElement>(null)
  const focusNewReport = useRef(false)
  function dismissRecheck() { setShowRecheck(false); recheckButton.current?.focus() }
  useEffect(() => {
    if (focusNewReport.current && run?.id === id) {
      reportHeading.current?.focus()
      focusNewReport.current = false
    }
  },[id,run?.id])
  const dialogRef = useRef<HTMLElement>(null)
  useEffect(() => {
    if (!showRecheck) return
    dialogRef.current?.querySelector<HTMLInputElement>('input')?.focus()
    function keydown(event: KeyboardEvent) {
      if (event.key === 'Escape' && !rechecking) dismissRecheck()
      if (event.key !== 'Tab') return
      const controls = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('input:not(:disabled),button:not(:disabled)') ?? [])
      const first = controls[0], last = controls.at(-1)
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', keydown)
    return () => document.removeEventListener('keydown', keydown)
  }, [showRecheck, rechecking])
  useEffect(() => {
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    setRun(null); setReviews([]); setShowRecheck(false); setError(''); requestID.current = newRequestID()
    async function load() {
      try {
        const value = await api<Run>(token, `/api/runs/${id}`, { signal: abort.signal })
        if (abort.signal.aborted) return
        setRun(value); setTarget(previous => previous || value.base_url); setError('')
        if (isActive(value.status)) timer = setTimeout(() => void load(), 1600)
        else onChanged()
      } catch (err) { if (!abort.signal.aborted) setError(errorText(err)) }
    }
    void load()
    void api<FindingReview[]>(token, `/api/runs/${id}/reviews`, { signal: abort.signal }).then(setReviews).catch(err => { if (!abort.signal.aborted) setError(errorText(err)) })
    return () => { abort.abort(); clearTimeout(timer); setTarget('') }
    // onChanged is a stable callback from the application shell.
  }, [token, id, revision, onChanged])
  useEffect(() => {
    setOriginal(null); setOriginalReviews([]); setComparisonError('')
    if (!run?.source_run_id) return
    const abort = new AbortController()
    void Promise.all([api<Run>(token, `/api/runs/${run.source_run_id}`, {signal:abort.signal}), api<FindingReview[]>(token, `/api/runs/${run.source_run_id}/reviews`, {signal:abort.signal})]).then(([source, reviews]) => {if (!abort.signal.aborted) {setOriginal(source);setOriginalReviews(reviews)}}).catch(err => {if (!abort.signal.aborted) setComparisonError(errorText(err))})
    return () => abort.abort()
  }, [token,run?.source_run_id])
  async function recheck(event: FormEvent) {
    event.preventDefault(); if (!run) return
    setRechecking(true); setError('')
    try { const next = await api<Run>(token, `/api/runs/${id}/recheck`, { method: 'POST', body: JSON.stringify({ request_id: requestID.current, base_url: target, mode: run.mode }) }); focusNewReport.current=true; setShowRecheck(false); onChanged(); onOpenRun(next.id) } catch (err) { setError(errorText(err)) } finally { setRechecking(false) }
  }
  async function cancel() {
    try { await api(token, `/api/runs/${id}/cancel`, { method: 'POST' }); setRevision(value => value + 1); onChanged() } catch (err) { setError(errorText(err)) }
  }
  const issue = run?.results.find(r => r.status === 'failed') ?? run?.results.find(r => r.status !== 'passed' && r.status !== 'running')
  const issueScenario = run?.scenarios.find(s => s.id === issue?.scenario_id)
  const issueReview = reviews.find(review => review.scenario_id === issue?.scenario_id)
  const counts = run ? runCounts(run) : null
  return <div className="flow-page run-report"><button className="link-button back-link" onClick={onBack}><ArrowLeft size={15} />All results</button>{error && <div className="error-banner" role="alert">{error}<button className="link-button" onClick={() => setRevision(value => value + 1)}>Retry</button></div>}{!run && !error && <p>Loading results…</p>}{run && counts && <><div className="page-heading"><div><div className="eyebrow">{run.source_run_id ? 'RECHECK RESULTS' : 'TEST RESULTS'}</div><h1 tabIndex={-1} ref={reportHeading}>{title || run.scenarios[0]?.name || 'App checks'}</h1><h2 className="run-status-title">{checkCount(run.scenarios.length)} · {run.status}</h2><p>{run.base_url} · {formatDate(run.created_at)}</p><p className="release-decision"><strong>{run.mode === 'blocking' ? 'Blocking check' : 'Advisory check'}</strong> · {run.gate === 'fail' ? 'Release blocked' : run.gate === 'warn' ? 'Release warning' : run.gate === 'pass' ? 'Gate passed' : 'Decision pending'}</p></div><span className={`pill pill-${run.status}`}>{run.status}</span></div>{issue && issueScenario && <section className="flow-card primary-finding" data-status={run.status === 'cancelled' ? 'cancelled' : issue.status}>
      <FailureSummary scenario={issueScenario} result={issue} run={run} token={token}/>
      {!isActive(run.status) && run.status !== 'cancelled' && <details className="review-disclosure"><summary>Review this finding</summary><Finding key={issue.scenario_id + (issueReview?.updated_at || '')} token={token} run={run} result={issue} initial={issueReview}/></details>}
    </section>}<section className="flow-card">{run.status === 'passed' && <><h2>Verified outcomes</h2><ul className="verified-outcomes">{run.scenarios.map(scenario => {
      const result = run.results.find(item => item.scenario_id === scenario.id)
      return <li key={scenario.id}><p>{scenario.expected_outcome}</p>{result && <ResultEvidence token={token} run={run} scenario={scenario} result={result}/>}</li>
    })}</ul></>}<div className="flow-counts"><span><strong>{counts.passed}</strong> passed</span><span><strong>{counts.failed}</strong> failed</span><span><strong>{counts.blocked + counts.error}</strong> unverified</span><span><strong>{counts.incomplete}</strong> unfinished</span></div><p>{isActive(run.status) ? 'Browser checks are in progress. Results update automatically.' : 'These results cover only the selected checks, on the target recorded above.'}</p>{run.source_run_id && <button className="link-button" onClick={() => onOpenRun(run.source_run_id!)}>View original run <ArrowRight size={14} /></button>}<div className="flow-row">{isActive(run.status) ? <button className="button button-outline" onClick={() => void cancel()}><Square size={14} />Cancel run</button> : <><button ref={recheckButton} className="button button-primary" onClick={() => { setTarget(run.base_url); setShowRecheck(true); requestID.current = newRequestID() }}><RefreshCw size={15} />Recheck a deployment</button><button className="button button-outline" onClick={() => onInvestigate(run)}>Discuss these results in Test</button></>}</div></section>
    {run.release && <section className="flow-card"><h2>Deployment · {run.release.deployment_key}</h2>{run.release.repositories.map(repo=><p key={repo.snapshot_id}><a className="link-button" href={snapshotHref(repo.snapshot_id)}>{repo.role} · {repo.repository} · {repo.commit_sha.slice(0,12)}</a></p>)}</section>}
    {run.source_run_id && <section className="flow-card run-comparison"><h2>What changed?</h2>{comparisonError && <p role="alert">{comparisonError}</p>}{!original && !comparisonError && <p>Loading the original run…</p>}{original && <><div className="comparison-targets"><p><strong>Before</strong><br />{original.base_url}<br />{formatDate(original.created_at)}</p><p><strong>This recheck</strong><br />{run.base_url}<br />{formatDate(run.created_at)}</p></div><p>The original approved expectations and steps are unchanged.</p><div className="comparison-list">{compareRuns(original,run).map(row=><article key={row.id}><strong>{row.name}</strong><span>{row.before} → {row.after}</span><span className={`pill pill-${row.changed==='Fixed'?'passed':row.after==='failed'?'failed':'neutral'}`}>{row.changed}</span></article>)}</div></>}</section>}
    {run.scenarios.some(s=>s.steps.some(step=>step.action==='phase')) && <details className="flow-card" open={isActive(run.status)}><summary>Journey stages</summary><JourneyProgress run={run}/></details>}<div className="result-list">{run.scenarios.map((scenario, i) => {
      const result = run.results.find(item => item.scenario_id === scenario.id)
      const cancelled = completionLabel(run, scenario.id) === 'Cancelled before completion'
      const interrupted = cancelled || (result?.status === 'running' && !isActive(run.status))
      return <details className="flow-card report-check" key={scenario.id} open={result?.status !== 'passed'}><summary><span><strong>{i + 1}. {scenario.name}</strong></span><span className={`pill pill-${interrupted ? 'neutral' : result?.status ?? 'pending'}`}>{cancelled ? 'Cancelled before completion' : interrupted ? 'Not completed' : result?.status ?? 'Not completed'}</span></summary><div className="expected-block"><small>APPROVED EXPECTATION</small><p>{scenario.expected_outcome}</p></div><p>{interrupted ? 'The run stopped before this journey completed. Review the stages below; cleanup may be unconfirmed.' : result?.message || (result?.status === 'passed' ? 'The approved assertions passed.' : 'No completed result was recorded.')}</p>{result && <>{result !== issue && run.status !== 'passed' && <ResultEvidence token={token} run={run} scenario={scenario} result={result}/>}<details className="step-report"><summary>Browser steps ({scenario.steps.length})</summary>{!result.step_results?.length && <p className="muted">No individual step records are available for this run.</p>}{scenario.steps.map((step, index) => { const execution = result.step_results?.find(item => item.index === index); return <div className="recorded-step" key={index}><span>{index + 1}</span><div><strong>{stepLabel(step)}</strong><details className="technical-step"><summary>Technical detail</summary><code>{step.action} {step.path || step.test_id || JSON.stringify(step.locator)}</code></details>{step.action === 'assert_text' && <p>Expected: “{step.value}”</p>}{execution?.observed !== undefined && <p className="actual-value">Observed: “{execution.observed}”</p>}{execution?.message && execution.status !== 'not_run' && <p>{execution.message}</p>}</div><span>{execution ? `${execution.status==='not_run'?(run.status==='cancelled'?'Skipped after cancellation':'Skipped after an earlier failure'):execution.status} · ${(execution.duration_ms / 1000).toFixed(1)}s` : result.step_results?.length ? 'Not reached' : 'Not recorded'}</span></div> })}</details>{originalReviews.find(review=>review.scenario_id===scenario.id) && <div className="original-finding"><strong>Original finding</strong><p>{originalReviews.find(review=>review.scenario_id===scenario.id)!.status} · {originalReviews.find(review=>review.scenario_id===scenario.id)!.owner || 'Unassigned'}</p><p>{originalReviews.find(review=>review.scenario_id===scenario.id)!.reason}</p><small>This is the original review, not a decision on this recheck.</small></div>}{result !== issue && result.status !== 'passed' && !isActive(run.status) && run.status !== 'cancelled' && <Finding key={`${scenario.id}:${reviews.find(review => review.scenario_id === scenario.id)?.updated_at ?? ''}`} token={token} run={run} result={result} initial={reviews.find(review => review.scenario_id === scenario.id)} />}</>}</details>
    })}</div>
    {showRecheck && <div className="flow-modal-backdrop"><section ref={dialogRef} className="flow-modal" role="dialog" aria-modal="true" aria-labelledby="recheck-title"><h2 id="recheck-title">Recheck the same {checkCount(run.scenarios.length)}</h2><p>Deploy the fix first, then provide its URL. We’ll run the original approved expectations and steps against that deployment.</p><p>Rechecking does not change code or adjust expectations.</p><form onSubmit={event => void recheck(event)}><label htmlFor="recheck-url">Deployed app URL</label><input id="recheck-url" type="url" required value={target} onChange={event => setTarget(event.target.value)} /><p className="muted">The server and browser worker must both allow this origin.</p>{error && <p role="alert">{error}</p>}<div className="flow-row"><button type="button" className="button button-outline" disabled={rechecking} onClick={dismissRecheck}>Cancel</button><button className="button button-primary" disabled={rechecking}>{rechecking ? 'Starting…' : `Run ${checkCount(run.scenarios.length)}`}</button></div></form></section></div>}
  </>}</div>
}
