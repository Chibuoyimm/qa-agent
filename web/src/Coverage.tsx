import { useEffect, useState, type FormEvent } from 'react'
import { ArrowRight, Plus, RefreshCw } from 'lucide-react'
import { api, formatDate, type CoverageData, type Project } from './api'
import { coverageGroups, errorText } from './workspace'

export default function Coverage({ token, project, revision, onChanged, onOpenRun, onTest }: { token: string; project: Project; revision: number; onChanged: () => void; onOpenRun: (id: string) => void; onTest: () => void }) {
  const [data, setData] = useState<CoverageData | null>(null)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [filter, setFilter] = useState('All journeys')
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
  const root = `/api/projects/${project.id}`
  useEffect(() => {
    const abort = new AbortController()
    void api<CoverageData>(token, `${root}/coverage`, { signal: abort.signal }).then(value => { setData(value); setError('') }).catch(err => { if (!abort.signal.aborted) setError(errorText(err)) })
    return () => abort.abort()
  }, [token, root, revision, refresh])
  async function add(event: FormEvent) {
    event.preventDefault(); setSaving(true); setError('')
    try { await api(token, `${root}/journeys`, { method: 'POST', body: JSON.stringify({ name }) }); setName(''); setRefresh(value => value + 1); onChanged() } catch (err) { setError(errorText(err)) } finally { setSaving(false) }
  }
  async function organize(scenarioId: string, journeyId: string) {
    try { await api(token, `${root}/scenarios/${scenarioId}/journey`, { method: 'POST', body: JSON.stringify({ journey_id: journeyId }) }); setRefresh(value => value + 1); onChanged() } catch (err) { setError(errorText(err)) }
  }
  const groups = data ? coverageGroups(data) : []
  const executed = data?.checks.filter(check => check.result.status === 'passed' || check.result.status === 'failed') ?? []
  const testedJourneys = groups.filter(group => group.id && group.executed > 0).length
  const untested = groups.filter(group => group.id && group.executed === 0)
  return <div className="flow-page"><div className="page-heading"><div><div className="eyebrow">COVERAGE</div><h1>What’s been checked?</h1><p>Latest results for unique checks. Repeated runs count once.</p></div><button className="button button-outline" onClick={() => setRefresh(value => value + 1)}><RefreshCw size={15} />Refresh</button></div>
    {error && <div className="error-banner" role="alert">{error}</div>}{!data && !error && <p>Loading coverage…</p>}
    {data && <><section className="coverage-overview flow-card"><div className="eyebrow">OBSERVED COVERAGE</div><h2>{executed.length} checks ran across {testedJourneys} {testedJourneys === 1 ? 'journey' : 'journeys'}</h2><div className="flow-counts"><span><strong>{executed.filter(check => check.result.status === 'passed').length}</strong> passed</span><span><strong>{executed.filter(check => check.result.status === 'failed').length}</strong> failed</span><span><strong>{data.checks.filter(check => check.result.status === 'blocked' || check.result.status === 'error').length}</strong> unverified</span><span><strong>{data.scenarios.filter(s => !data.checks.some(check => check.scenario.id === s.id)).length}</strong> never run</span></div><p className="muted">Recorded targets: {[...new Set(data.checks.map(check => check.base_url))].slice(0, 3).join(' · ') || 'No recorded results yet'}</p><p>Results prove the selected outcomes on the recorded target and date. They don’t establish that the entire app works.</p></section>
      {untested.length > 0 && <section className="coverage-gap"><strong>{untested.length} {untested.length === 1 ? 'journey still untested' : 'journeys still untested'}</strong><p>{untested.map(group => group.name).join(' · ')}</p><button className="link-button" onClick={onTest}>Plan the next checks <ArrowRight size={15} /></button></section>}
      <div className="flow-row coverage-toolbar"><div className="flow-tabs">{['All journeys', 'Untested', 'With results'].map(item => <button className={filter === item ? 'active' : ''} key={item} onClick={() => setFilter(item)}>{item}</button>)}</div><span>{data.journeys.length} journeys tracked</span></div>
      <div className="journey-list">{groups.filter(group => filter === 'All journeys' || (filter === 'Untested' ? Boolean(group.id) && group.executed === 0 : group.checks.some(check => check.latest))).map(group => <details className="flow-card journey-card" key={group.id} open={group.executed === 0}><summary><span><strong>{group.name}</strong><small>{group.executed} checks ran · {group.scenarios.length} defined</small></span><span className={`pill ${group.executed ? 'pill-passed' : 'pill-pending'}`}>{!group.id ? 'Not organized' : group.executed ? 'Has results' : 'Untested'}</span></summary>{!group.id && <p>These checks have no journey assigned. Access checks used during app exploration may not have a separate test-run result.</p>}{!group.checks.length && <p>No checks have been defined for this journey. Start a test request and select this journey.</p>}{group.checks.map(({ scenario, latest }) => <article className="coverage-check" key={scenario.id}><div className="flow-row"><strong>{scenario.name}</strong><span className={`pill pill-${latest?.result.status ?? 'pending'}`}>{latest?.result.status ?? 'Never run'}</span></div><p>{latest?.scenario.expected_outcome ?? scenario.expected_outcome}</p>{latest ? <><small>{latest.base_url} · {formatDate(latest.finished_at)}</small><button className="link-button" onClick={() => onOpenRun(latest.run_id)}>View latest result <ArrowRight size={14} /></button></> : <small>{scenario.approved ? 'Approved; awaiting its first run' : 'Draft; approval needed before running'}</small>}{!group.id && <label className="organize-check">Move to journey<select defaultValue="" onChange={event => void organize(scenario.id, event.target.value)}><option value="" disabled>Choose journey</option>{data.journeys.map(journey => <option key={journey.id} value={journey.id}>{journey.name}</option>)}</select></label>}</article>)}</details>)}</div>
      {data.journeys.length === 0 && <div className="flow-card"><h2>Track the journeys that matter</h2><p>Add journeys such as sign-in, checkout, or reporting. They stay visibly untested until checks actually run.</p></div>}
      <form className="flow-card add-journey" onSubmit={event => void add(event)}><label htmlFor="journey-name">Add a journey to track</label><div className="flow-row"><input id="journey-name" required maxLength={200} placeholder="e.g. Invite a teammate" value={name} onChange={event => setName(event.target.value)} /><button className="button button-outline" disabled={saving || !name.trim()}><Plus size={16} />{saving ? 'Adding…' : 'Add journey'}</button></div></form>
    </>}
  </div>
}
