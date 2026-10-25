import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ArrowLeft, ArrowRight, RefreshCw, Square, Video } from 'lucide-react'
import { api, formatDate, isActive, newRequestID, type Artifact, type FindingReview, type Run, type Scenario, type ScenarioResult } from './api'
import { errorText, runCounts, compareRuns, checkCount, stepLabel } from './workspace'

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
  return <div className="evidence"><button className="button button-outline" onClick={() => setOpened(previous => !previous)}><Video size={15} />{opened ? 'Hide' : 'Open'} {artifact.kind === 'video' ? 'browser recording' : 'final screenshot'}</button>{opened && <>{loading && <p>Loading evidence…</p>}{error && <p role="alert">{error}</p>}{url && (artifact.kind === 'video' ? <video controls preload="metadata" src={url} aria-label={`Browser recording for ${scenario.name}`} /> : <img src={url} alt={`Final browser view for ${scenario.name}`} />)}</>}</div>
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

export default function RunReport({ token, id, onBack, onOpenRun, onInvestigate, onChanged, title }: { token: string; id: string; onBack: () => void; onOpenRun: (id: string) => void; onInvestigate: (run: Run) => void; onChanged: () => void; title?: string }) {
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
  const dialogRef = useRef<HTMLElement>(null)
  useEffect(() => {
    if (!showRecheck) return
    dialogRef.current?.querySelector<HTMLInputElement>('input')?.focus()
    function keydown(event: KeyboardEvent) {
      if (event.key === 'Escape' && !rechecking) setShowRecheck(false)
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
    try { const next = await api<Run>(token, `/api/runs/${id}/recheck`, { method: 'POST', body: JSON.stringify({ request_id: requestID.current, base_url: target, mode: run.mode }) }); setShowRecheck(false); onChanged(); onOpenRun(next.id) } catch (err) { setError(errorText(err)) } finally { setRechecking(false) }
  }
  async function cancel() {
    try { await api(token, `/api/runs/${id}/cancel`, { method: 'POST' }); setRevision(value => value + 1); onChanged() } catch (err) { setError(errorText(err)) }
  }
  const counts = run ? runCounts(run) : null
  return <div className="flow-page"><button className="link-button back-link" onClick={onBack}><ArrowLeft size={15} />All results</button>{error && <div className="error-banner" role="alert">{error}<button className="link-button" onClick={() => setRevision(value => value + 1)}>Retry</button></div>}{!run && !error && <p>Loading results…</p>}{run && counts && <><div className="page-heading"><div><div className="eyebrow">{run.source_run_id ? 'RECHECK RESULTS' : 'TEST RESULTS'}</div><h1>{title || run.scenarios[0]?.name || 'App checks'}</h1><h2 className="run-status-title">{checkCount(run.scenarios.length)} · {run.status}</h2><p>{run.base_url} · {formatDate(run.created_at)}</p></div><span className={`pill pill-${run.status}`}>{run.status}</span></div><section className="flow-card"><div className="flow-counts"><span><strong>{counts.passed}</strong> passed</span><span><strong>{counts.failed}</strong> failed</span><span><strong>{counts.blocked + counts.error}</strong> unverified</span><span><strong>{counts.incomplete}</strong> unfinished</span></div><p>{isActive(run.status) ? 'Browser checks are in progress. Results update automatically.' : 'These results cover only the selected checks, on the target recorded above.'}</p>{run.source_run_id && <button className="link-button" onClick={() => onOpenRun(run.source_run_id!)}>View original run <ArrowRight size={14} /></button>}<div className="flow-row">{isActive(run.status) ? <button className="button button-outline" onClick={() => void cancel()}><Square size={14} />Cancel run</button> : <><button className="button button-primary" onClick={() => { setTarget(run.base_url); setShowRecheck(true); requestID.current = newRequestID() }}><RefreshCw size={15} />Recheck a deployment</button><button className="button button-outline" onClick={() => onInvestigate(run)}>Discuss these results in Test</button></>}</div></section>
    {run.source_run_id && <section className="flow-card run-comparison"><h2>What changed?</h2>{comparisonError && <p role="alert">{comparisonError}</p>}{!original && !comparisonError && <p>Loading the original run…</p>}{original && <><div className="comparison-targets"><p><strong>Before</strong><br />{original.base_url}<br />{formatDate(original.created_at)}</p><p><strong>This recheck</strong><br />{run.base_url}<br />{formatDate(run.created_at)}</p></div><p>The original approved expectations and steps are unchanged.</p><div className="comparison-list">{compareRuns(original,run).map(row=><article key={row.id}><strong>{row.name}</strong><span>{row.before} → {row.after}</span><span className={`pill pill-${row.changed==='Fixed'?'passed':row.after==='failed'?'failed':'neutral'}`}>{row.changed}</span></article>)}</div></>}</section>}
    <div className="result-list">{run.scenarios.map((scenario, i) => {
      const result = run.results.find(item => item.scenario_id === scenario.id)
      return <details className="flow-card report-check" key={scenario.id} open={result?.status !== 'passed'}><summary><span><strong>{i + 1}. {scenario.name}</strong></span><span className={`pill pill-${result?.status ?? 'pending'}`}>{result?.status ?? 'Not completed'}</span></summary><div className="expected-block"><small>APPROVED EXPECTATION</small><p>{scenario.expected_outcome}</p></div><p>{result?.message || (result?.status === 'passed' ? 'The approved assertions passed.' : 'No completed result was recorded.')}</p>{result && <><div className="step-report"><h4>Browser steps</h4>{!result.step_results?.length && <p className="muted">No individual step records are available for this run.</p>}{scenario.steps.map((step, index) => { const execution = result.step_results?.find(item => item.index === index); return <div className="recorded-step" key={index}><span>{index + 1}</span><div><strong>{stepLabel(step)}</strong><details className="technical-step"><summary>Technical detail</summary><code>{step.action} {step.path || step.test_id || JSON.stringify(step.locator)}</code></details>{step.action === 'assert_text' && <p>Expected: “{step.value}”</p>}{execution?.observed !== undefined && <p className="actual-value">Observed: “{execution.observed}”</p>}{execution?.message && <p>{execution.message}</p>}</div><span>{execution ? `${execution.status} · ${(execution.duration_ms / 1000).toFixed(1)}s` : result.step_results?.length ? 'Not reached' : 'Not recorded'}</span></div> })}</div><div className="evidence-list">{result.artifacts.length ? result.artifacts.map((artifact, index) => <Evidence key={index} token={token} run={run} scenario={scenario} artifact={artifact} index={index} />) : <p className="muted">No browser evidence was saved for this check.</p>}</div>{originalReviews.find(review=>review.scenario_id===scenario.id) && <div className="original-finding"><strong>Original finding</strong><p>{originalReviews.find(review=>review.scenario_id===scenario.id)!.status} · {originalReviews.find(review=>review.scenario_id===scenario.id)!.owner || 'Unassigned'}</p><p>{originalReviews.find(review=>review.scenario_id===scenario.id)!.reason}</p><small>This is the original review, not a decision on this recheck.</small></div>}{result.status !== 'passed' && <Finding key={`${scenario.id}:${reviews.find(review => review.scenario_id === scenario.id)?.updated_at ?? ''}`} token={token} run={run} result={result} initial={reviews.find(review => review.scenario_id === scenario.id)} />}</>}</details>
    })}</div>
    {showRecheck && <div className="flow-modal-backdrop"><section ref={dialogRef} className="flow-modal" role="dialog" aria-modal="true" aria-labelledby="recheck-title"><h2 id="recheck-title">Recheck the same {checkCount(run.scenarios.length)}</h2><p>Deploy the fix first, then provide its URL. We’ll run the original approved expectations and steps against that deployment.</p><p>Rechecking does not change code or adjust expectations.</p><form onSubmit={event => void recheck(event)}><label htmlFor="recheck-url">Deployed app URL</label><input id="recheck-url" type="url" required value={target} onChange={event => setTarget(event.target.value)} /><p className="muted">The server and browser worker must both allow this origin.</p>{error && <p role="alert">{error}</p>}<div className="flow-row"><button type="button" className="button button-outline" disabled={rechecking} onClick={() => setShowRecheck(false)}>Cancel</button><button className="button button-primary" disabled={rechecking}>{rechecking ? 'Starting…' : `Run ${checkCount(run.scenarios.length)}`}</button></div></form></section></div>}
  </>}</div>
}
