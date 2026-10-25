import { useState } from 'react'
import { formatDate, type Journey, type Run, type TestingTask } from './api'
import { checkCount, groupRuns, runCounts, runTitle } from './workspace'

export default function RunHistory({runs,tasks,journeys,onOpen,onRefresh,onTest}:{runs:Run[];tasks:TestingTask[];journeys:Journey[];onOpen:(id:string)=>void;onRefresh:()=>void;onTest:()=>void}) {
  const [query,setQuery]=useState('')
  const [status,setStatus]=useState('all')
  const groups=groupRuns(runs).filter(group=>group.some(run=>(status==='all'||run.status===status)&&runTitle(run,tasks,journeys).toLowerCase().includes(query.toLowerCase())))
  return <div className="flow-page"><div className="page-heading"><div><div className="eyebrow">RESULTS</div><h1>Your testing history</h1><p>Find a journey and follow its original run and rechecks together.</p></div><button className="button button-outline" onClick={onRefresh}>Refresh</button></div>
    <div className="flow-card task-options history-filters"><label>Find a journey or request<input value={query} onChange={e=>setQuery(e.target.value)} placeholder="e.g. Checkout" /></label><label>Groups with outcome<select value={status} onChange={e=>setStatus(e.target.value)}><option value="all">All outcomes</option>{['passed','failed','blocked','error','cancelled','queued','running'].map(s=><option key={s}>{s}</option>)}</select></label></div>
    {status !== 'all' && <p className="muted">Matching groups include their other attempts for context.</p>}
    {!runs.length && <section className="flow-card"><h2>No runs yet</h2><p>Approve a test plan to see its evidence here.</p><button className="button button-primary" onClick={onTest}>Start testing</button></section>}
    {runs.length>0&&!groups.length&&<p>No runs match these filters.</p>}
    {groups.map(group=><section className="flow-card history-group" key={group.at(-1)!.id}><h2>{runTitle(group[0],tasks,journeys)}</h2><p>{group.length} {group.length===1?'run':'runs'} · most recent first</p>{group.map(run=>{const c=runCounts(run);return <button className="history-attempt" key={run.id} onClick={()=>onOpen(run.id)}><div className="flow-row"><strong>{run.source_run_id?'Recheck':'Original run'} · {checkCount(run.scenarios.length)}</strong><span className={`pill pill-${run.status}`}>{run.status}</span></div><p>{c.passed} passed · {c.failed} failed · {c.blocked+c.error} unverified · {c.incomplete} unfinished</p><small>{run.base_url} · {formatDate(run.created_at)}</small></button>})}</section>)}
    {runs.length>=100&&<p className="muted">Showing the latest 100 runs. Older evidence remains available from Coverage and saved run links.</p>}
  </div>
}
