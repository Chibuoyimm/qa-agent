import { useEffect, useState, type FormEvent } from 'react'
import { ArrowRight, Plus, RefreshCw } from 'lucide-react'
import { api, formatDate, type CoverageData, type Project } from './api'
import { coverageGroups, errorText, stepLabel } from './workspace'

export default function Coverage({ token, project, revision, selectedJourney, onChanged, onOpenRun, onTest }: { token: string; project: Project; revision: number; selectedJourney?: string; onChanged: () => void; onOpenRun: (id: string) => void; onTest: (journey: string) => void }) {
  const [data, setData] = useState<CoverageData | null>(null)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [target, setTarget] = useState('')
  const [filter, setFilter] = useState('All journeys')
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
  const root = `/api/projects/${project.id}`
  useEffect(() => {
    const abort = new AbortController()
    void api<CoverageData>(token, `${root}/coverage?target=${encodeURIComponent(target)}`, { signal: abort.signal }).then(value => { setData(value); setError('') }).catch(err => { if (!abort.signal.aborted) setError(errorText(err)) })
    return () => abort.abort()
  }, [token, root, revision, refresh, target])
  const selectedLoaded = Boolean(selectedJourney && data?.journeys.some(j => j.id === selectedJourney))
  useEffect(() => {
    if (selectedLoaded) document.getElementById(`journey-${selectedJourney}`)?.focus()
  }, [selectedJourney, selectedLoaded])
  async function add(event: FormEvent) {
    event.preventDefault(); setSaving(true); setError('')
    try { await api(token, `${root}/journeys`, { method: 'POST', body: JSON.stringify({ name }) }); setName(''); setRefresh(value => value + 1); onChanged() } catch (err) { setError(errorText(err)) } finally { setSaving(false) }
  }
  async function organize(scenarioId: string, journeyId: string) {
    try { await api(token, `${root}/scenarios/${scenarioId}/journey`, { method: 'POST', body: JSON.stringify({ journey_id: journeyId }) }); setRefresh(value => value + 1); onChanged() } catch (err) { setError(errorText(err)) }
  }
  const groups = data ? coverageGroups(data) : []
  const attempts = data?.checks ?? []
  const verified = attempts.filter(check => check.last_verified || ['passed', 'failed'].includes(check.result.status)).length
  const testedJourneys = groups.filter(group => group.id && group.executed > 0).length
  const untested = groups.filter(group => group.id && group.attempted === 0)
  const bucket = (status?: string) => status === 'passed' ? 'Passed' : status === 'failed' ? 'Failed' : status ? 'Inconclusive' : 'Never attempted'
  const buckets = ['Passed','Failed','Inconclusive','Never attempted']
  const outcomes = buckets.map(label => ({
    label,
    count: label === 'Never attempted' ? groups.reduce((n, group) => n + group.checks.filter(check => !check.latest).length, 0) : attempts.filter(check => bucket(check.result.status) === label).length
  }))
  const visibleGroups = groups.map(group => ({
    ...group,
    checks: buckets.includes(filter) ? group.checks.filter(({latest})=>bucket(latest?.result.status) === filter) : group.checks
  })).filter(group => {
    if (filter === 'Untested') return group.id && !group.attempted
    if (filter === 'With results') return group.attempted > 0
    if (buckets.includes(filter)) return group.checks.length > 0
    return true
  })
  return <div className="flow-page"><div className="page-heading"><div><h1>What’s been checked?</h1><p>Latest results for unique checks. Repeated runs count once.</p></div><button className="button button-outline" onClick={() => setRefresh(value => value + 1)}><RefreshCw size={15} />Refresh</button></div>
    {error && <div className="error-banner" role="alert">{error}</div>}{!data && !error && <p>Loading coverage…</p>}
    <label className="target-filter">Deployment target<select value={target} onChange={e => {setData(null);setTarget(e.target.value);setFilter('All journeys')}}><option value="">All targets</option>{[...new Set([project.base_url,...(target ? [target] : []),...(data?.targets ?? [])])].map(url => <option key={url}>{url}</option>)}</select></label>
    {data && <><section className="coverage-overview flow-card"><h2>{verified} {verified === 1 ? 'check' : 'checks'} ran across {testedJourneys} {testedJourneys === 1 ? 'journey' : 'journeys'}</h2>
      <p>Latest attempt for each check · {target || 'All targets'}</p>
      <div className="coverage-meter" aria-hidden="true">{outcomes.filter(outcome => outcome.count > 0).map(({label,count}) =>
        <span key={label} data-outcome={label} style={{flexGrow:count}} />)}</div>
      <div className="coverage-counts">{outcomes.map(({label,count}) =>
        <button key={label} data-outcome={label} aria-pressed={filter === label} onClick={() => setFilter(filter === label ? 'All journeys' : label)}>
          <strong>{count}</strong><span>{label}</span>
        </button>)}</div></section>
      {untested.length > 0 && <section className="coverage-gap"><strong>{untested.length} {untested.length === 1 ? 'journey still untested' : 'journeys still untested'}</strong><p>{untested.map(group => group.name).join(' · ')}</p><p>Choose “Test this journey” below to plan checks for a specific gap.</p></section>}
      <div className="flow-row coverage-toolbar"><div className="flow-tabs">{['All journeys', 'Untested', 'With results', 'Inconclusive'].map(item => <button className={filter === item ? 'active' : ''} key={item} aria-pressed={filter === item} onClick={() => setFilter(item)}>{item}</button>)}</div><span>{data.journeys.length} journeys tracked</span></div>
      {visibleGroups.length === 0 && <p>No journeys match this filter.</p>}<div className="journey-list">{visibleGroups.map(group => <details className="flow-card journey-card" key={group.id} open={group.id === selectedJourney || group.executed === 0 || buckets.includes(filter)}><summary id={`journey-${group.id}`}><span><strong>{group.name}</strong><small>{group.executed} {group.executed === 1 ? 'check' : 'checks'} ran · {group.scenarios.length} defined</small></span><span className={`pill ${group.executed ? 'pill-neutral' : 'pill-pending'}`}>{!group.id ? 'Not organized' : group.checks.some(c=>c.latest && bucket(c.latest.result.status)==='Inconclusive') ? 'Includes inconclusive attempts' : group.executed ? `${group.checks.filter(c=>c.latest?.result.status==='failed').length} failed · ${group.checks.filter(c=>c.latest?.result.status==='passed').length} passed` : group.attempted ? 'Latest attempt inconclusive' : 'Untested'}</span></summary>{group.saved_plan && <div className="saved-journey-plan">
        <strong>Expected outcome</strong><p>{group.saved_plan.expected_outcome}</p>
        {Boolean(group.saved_plan.missing_setup?.length) && <><strong>{group.checks.length ? 'Setup recorded in the source plan' : 'Needs setup'}</strong><ul>{group.saved_plan.missing_setup!.map((item,i) => <li key={i}>{item}</li>)}</ul></>}
        {Boolean(group.saved_plan.steps?.length) && <details><summary>{group.saved_plan.steps!.length} draft steps</summary><ol>{group.saved_plan.steps!.map((step,i) => <li key={i}>{stepLabel(step)}</li>)}</ol></details>}
        <a href={`#/test?project=${project.id}&task=${group.saved_plan.task_id}`}>Open source plan</a>
      </div>}{group.id && <button className="link-button journey-test" onClick={() => onTest(group.id)}>Test this journey <ArrowRight size={15} /></button>}{!group.id && <p>These checks have no journey assigned. Access checks used during app exploration may not have a separate test-run result.</p>}{!group.checks.length && !group.saved_plan && <p>No checks have been defined for this journey. Start a test request and select this journey.</p>}{group.checks.map(({ scenario, latest }) => <article className="coverage-check" key={scenario.id}><div className="flow-row"><strong>{scenario.name}</strong><span className={`pill pill-${latest?.result.status ?? 'pending'}`}>{latest?.run_status === 'cancelled' && !['passed','failed'].includes(latest.result.status) ? 'Cancelled' : latest?.result.status ?? 'Never run'}</span></div><p>{latest?.scenario.expected_outcome ?? scenario.expected_outcome}</p>{latest ? <><small>{latest.base_url} · {formatDate(latest.finished_at)}</small><button className="link-button" onClick={() => onOpenRun(latest.run_id)}>View latest result <ArrowRight size={14} /></button>{latest.last_verified && latest.last_verified.run_id !== latest.run_id && <div className="prior-proof"><strong>Previously {latest.last_verified.status}</strong><small>{latest.last_verified.base_url} · {formatDate(latest.last_verified.finished_at)}</small><button className="link-button" onClick={() => onOpenRun(latest.last_verified!.run_id)}>View last verified result</button></div>}</> : <small>{scenario.approved ? 'Approved; awaiting its first run' : 'Draft; approval needed before running'}</small>}{!group.id && <label className="organize-check">Move to journey<select defaultValue="" onChange={event => void organize(scenario.id, event.target.value)}><option value="" disabled>Choose journey</option>{data.journeys.map(journey => <option key={journey.id} value={journey.id}>{journey.name}</option>)}</select></label>}</article>)}</details>)}</div>
      {data.journeys.length === 0 && <div className="flow-card"><h2>Track the journeys that matter</h2><p>Add journeys such as sign-in, checkout, or reporting. They stay visibly untested until checks actually run.</p></div>}
      <form className="flow-card add-journey" onSubmit={event => void add(event)}><label htmlFor="journey-name">Add a journey to track</label><div className="flow-row"><input id="journey-name" required maxLength={200} placeholder="e.g. Invite a teammate" value={name} onChange={event => setName(event.target.value)} /><button className="button button-outline" disabled={saving || !name.trim()}><Plus size={16} />{saving ? 'Adding…' : 'Add journey'}</button></div></form>
    </>}
  </div>
}
