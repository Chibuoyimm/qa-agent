import { useState } from 'react'
import { ChevronRight, RefreshCw, Search } from 'lucide-react'
import { formatDate, type Journey, type Run, type TestingTask } from './api'
import { checkCount, groupRuns, runCounts, runTitle } from './workspace'

type Props = {
  runs: Run[]
  tasks: TestingTask[]
  journeys: Journey[]
  onOpen: (id: string) => void
  onRefresh: () => void
  onTest: () => void
}

export default function RunHistory({ runs, tasks, journeys, onOpen, onRefresh, onTest }: Props) {
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState('all')
  const term = query.trim().toLowerCase()
  const taskTitles = new Map(tasks.map(task => [task.id, task.title]))
  const groups = groupRuns(runs).filter(group => group.some(run =>
    (status === 'all' || run.status === status) &&
    [runTitle(run, tasks, journeys), taskTitles.get(run.task_id ?? ''), ...run.scenarios.map(s => s.name)].some(name => name?.toLowerCase().includes(term))
  ))

  return <div className="flow-page results-page">
    <header className="page-heading"><div><h1>Your testing history</h1><p>Find a journey and follow its original run and rechecks together.</p></div>
      <button className="button button-outline" onClick={onRefresh}><RefreshCw size={15} />Refresh</button>
    </header>
    <div className="task-options history-filters">
      <label>Find a journey or request<span className="search-control"><Search size={16} aria-hidden="true" /><input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search requests and journeys" /></span></label>
      <label>Groups with outcome<select value={status} onChange={event => setStatus(event.target.value)}><option value="all">All outcomes</option>{['passed','failed','blocked','error','cancelled','queued','running'].map(value => <option key={value}>{value}</option>)}</select></label>
    </div>
    {status !== 'all' && <p className="muted">Matching groups include their other attempts for context.</p>}
    {!runs.length && <section className="flow-card"><h2>No runs yet</h2><p>Approve a test plan to see its evidence here.</p><button className="button button-primary" onClick={onTest}>Start testing</button></section>}
    {runs.length > 0 && !groups.length && <p>No runs match these filters.</p>}
    {groups.map(group => {
      const title = runTitle(group[0], tasks, journeys)
      return <section className="flow-card history-group" key={group.at(-1)!.id}>
        <header><h2>{title}</h2><span>{group.length} {group.length === 1 ? 'run' : 'runs · most recent first'}</span></header>
        {group.map(run => {
          const counts = runCounts(run)
          const request = taskTitles.get(run.task_id ?? '')
          return <button className="history-attempt" key={run.id} onClick={() => onOpen(run.id)}>
            <span className={`pill pill-${run.status}`}>{run.status}</span>
            <span className="attempt-name"><strong>{run.source_run_id ? 'Recheck' : 'Original run'} · {checkCount(run.scenarios.length)}</strong>
              {request && request !== title && <small>{request}</small>}
              <small>{counts.passed} passed · {counts.failed} failed{counts.blocked + counts.error > 0 && ` · ${counts.blocked + counts.error} unverified`}{counts.incomplete > 0 && ` · ${counts.incomplete} unfinished`}</small>
            </span>
            <span className="attempt-target"><span>{formatDate(run.created_at)}</span><small>{run.base_url}</small></span>
            <ChevronRight size={16} aria-hidden="true" />
          </button>
        })}
      </section>
    })}
    {runs.length >= 100 && <p className="muted">Showing the latest 100 runs. Older evidence remains available from Coverage and saved run links.</p>}
  </div>
}
