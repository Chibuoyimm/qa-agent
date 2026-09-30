import { useEffect, useState } from 'react'
import {
  AlertCircle, ArrowRight, CheckCircle2, ChevronDown, Clock3, Code2,
  ExternalLink, FolderOpen, GitCommitHorizontal, LoaderCircle, RefreshCw,
  Rocket, ShieldX,
} from 'lucide-react'
import { api, formatDate, isActive, shortId, type Release, type ScenarioResult } from './api'

const statusLabel: Record<string, string> = {
  queued: 'Queued', running: 'Running', passed: 'Passed', failed: 'Failed',
  blocked: 'Blocked', error: 'Error', cancelled: 'Cancelled',
  pending: 'Pending', pass: 'Pass', warn: 'Warning', fail: 'Fail',
}

function Status({ value }: { value: string }) {
  const icon = value === 'passed' || value === 'pass' ? <CheckCircle2 size={13} />
    : value === 'running' ? <LoaderCircle size={13} className="spin" />
      : value === 'failed' || value === 'fail' ? <ShieldX size={13} />
        : value === 'error' ? <AlertCircle size={13} /> : <Clock3 size={13} />
  return <span className={`pill pill-${value}`}>{icon}{statusLabel[value] ?? value}</span>
}

function Result({ result }: { result: ScenarioResult }) {
  return <>
    <p className="result-message">{result.message || (result.status === 'passed' ? 'Assertion passed.' : 'No finding was provided.')}</p>
    <div className="result-meta">Duration {result.duration_ms < 1000 ? `${result.duration_ms} ms` : `${(result.duration_ms / 1000).toFixed(1)} s`}</div>
    {result.artifacts?.length > 0 && <div className="artifacts"><div className="field-label">Local worker files</div>
      {result.artifacts.map((artifact, index) => <div className="artifact" key={`${artifact.path}-${index}`}><FolderOpen size={15} /><span>{artifact.kind}</span><code>{artifact.path}</code></div>)}
      <p>Paths are relative to the worker’s artifact directory. They are metadata, not hosted links.</p>
    </div>}
  </>
}

export default function Releases({ token, projectId, onOpenSnapshot }: {
  token: string
  projectId: string
  onOpenSnapshot: (id: string) => void
}) {
  const [releases, setReleases] = useState<Release[]>([])
  const [selectedId, setSelectedId] = useState('')
  const [selected, setSelected] = useState<Release | null>(null)
  const [loading, setLoading] = useState(true)
  const [detailLoading, setDetailLoading] = useState(false)
  const [error, setError] = useState('')
  const [refreshKey, setRefreshKey] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    void api<Release[]>(token, `/api/projects/${encodeURIComponent(projectId)}/releases`, { signal: controller.signal })
      .then(items => {
        if (controller.signal.aborted) return
        setReleases(items)
        setSelectedId(current => items.some(item => item.id === current) ? current : items[0]?.id ?? '')
      })
      .catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load releases.') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [token, projectId, refreshKey])

  useEffect(() => {
    if (!selectedId) { setSelected(null); return }
    const controller = new AbortController()
    setDetailLoading(true)
    void api<Release>(token, `/api/projects/${encodeURIComponent(projectId)}/releases/${encodeURIComponent(selectedId)}`, { signal: controller.signal })
      .then(item => { if (!controller.signal.aborted && item.project_id === projectId && item.id === selectedId) setSelected(item) })
      .catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load release details.') })
      .finally(() => { if (!controller.signal.aborted) setDetailLoading(false) })
    return () => controller.abort()
  }, [token, projectId, selectedId, refreshKey])

  const active = releases.some(item => isActive(item.run.status))
  useEffect(() => {
    if (!active || error) return
    const timer = window.setInterval(() => setRefreshKey(value => value + 1), 3000)
    return () => window.clearInterval(timer)
  }, [active, error])

  function refresh() { setError(''); setLoading(releases.length === 0); setRefreshKey(value => value + 1) }

  const release = selected?.id === selectedId ? selected : null
  const run = release?.run
  const results = new Map(run?.results?.map(result => [result.scenario_id, result]) ?? [])
  const counts = { passed: 0, failed: 0, blocked: 0, error: 0 }
  run?.results?.forEach(result => { counts[result.status] += 1 })
  const incomplete = run ? Math.max(0, run.scenarios.length - (run.results?.length ?? 0)) : 0

  return <>
    <div className="page-heading"><div><div className="eyebrow">DEPLOYMENT PROOF</div><h1>Releases</h1><p>See the deployed target, exact source revisions, and the checks behind each gate.</p></div>
      <button className="button button-outline" onClick={refresh} aria-label="Refresh releases"><RefreshCw size={16} />Refresh</button></div>
    {error && <div className="error-banner" role="alert"><AlertCircle size={18} /><span>{error}. Results may be out of date.</span><button className="text-button" onClick={refresh}>Retry</button></div>}
    {loading ? <div className="loading-state"><LoaderCircle size={20} className="spin" />Loading releases…</div>
      : releases.length === 0 ? <div className="empty-state"><div className="empty-icon"><Rocket size={28} /></div><h3>No releases yet</h3><div className="empty-copy">A release appears here when a deployment pipeline submits its target, source revisions, and approved checks.</div></div>
        : <div className="releases-layout"><aside className="releases-list" aria-label="Release history"><div className="releases-list-head"><h2>Recent releases</h2><span>{releases.length} latest</span></div>
          {releases.map(item => <button className={`release-list-item ${item.id === selectedId ? 'active' : ''}`} key={item.id} onClick={() => setSelectedId(item.id)} aria-current={item.id === selectedId ? 'true' : undefined}>
            <span className="release-list-key">{item.deployment_key}</span><span className="release-list-meta"><Status value={item.run.gate} /><span>{formatDate(item.created_at)}</span></span><small>{item.base_url}</small>
          </button>)}</aside>
          <section className="panel release-detail" aria-label="Release details">
            {detailLoading && !release ? <div className="loading-state"><LoaderCircle size={20} className="spin" />Loading release details…</div>
              : !release ? <div className="quiet-empty">Select a release to view its evidence.</div>
                : <>
                  <div className="release-head"><div><div className="eyebrow">RELEASE · {shortId(release.id)}</div><h2>{release.deployment_key}</h2><p>Created {formatDate(release.created_at)}</p></div><div className="release-head-gate"><span>Deployment gate</span><Status value={run!.gate} /></div></div>
                  <div className="release-target"><span className="release-fact-label">DEPLOYMENT TARGET</span><a href={release.base_url} target="_blank" rel="noopener noreferrer">{release.base_url}<ExternalLink size={14} /></a><small>Checks target this release URL.</small></div>
                  <div className="release-facts"><div><span>Mode</span><strong>{release.mode === 'blocking' ? 'Blocking gate' : 'Advisory gate'}</strong></div><div><span>Run</span><strong><code title={run!.id}>{run!.id}</code></strong></div><div><span>Run status</span><Status value={run!.status} /></div><div><span>Gate</span><Status value={run!.gate} /></div></div>
                  <section className="release-section"><div className="release-section-head"><h3>Recorded source revisions</h3><span>{release.repositories.length} {release.repositories.length === 1 ? 'repository' : 'repositories'}</span></div>
                    <div className="release-repositories">{release.repositories.map(source => <article key={source.snapshot_id} className="release-repository"><div className="release-source-head"><span className="repository-role">{source.role}</span><strong>{source.repository}</strong></div><div className="release-source-line"><GitCommitHorizontal size={15} /><span>Commit</span><code title={source.commit_sha}>{source.commit_sha}</code></div><div className="release-source-line"><Code2 size={15} /><span>Content SHA-256</span><code title={source.content_sha256}>{source.content_sha256}</code></div><div className="release-source-paths"><span>Selected paths</span><ul>{source.paths.map(path => <li key={path}><code>{path}</code></li>)}</ul></div><button className="link-button" onClick={() => onOpenSnapshot(source.snapshot_id)}>Review saved snapshot <ArrowRight size={14} /></button></article>)}</div>
                  </section>
                  <section className="release-section"><div className="release-section-head"><h3>Scenario results</h3><span>{run!.scenarios.length} selected</span></div>
                    <div className="report-grid"><div><span>Selected</span><strong>{run!.scenarios.length}</strong></div><div><span>Passed</span><strong className="tone-pass">{counts.passed}</strong></div><div><span>Failed</span><strong className="tone-fail">{counts.failed}</strong></div><div><span>Blocked</span><strong className="tone-block">{counts.blocked}</strong></div><div><span>Error</span><strong className="tone-error">{counts.error}</strong></div><div><span>Incomplete</span><strong>{incomplete}</strong></div></div>
                    <div className="run-facts"><span>Started: {formatDate(run!.started_at)}</span><span>Finished: {formatDate(run!.finished_at)}</span></div>
                    {incomplete > 0 && <div className="incomplete-note"><Clock3 size={16} /><span>{incomplete} selected {incomplete === 1 ? 'scenario has' : 'scenarios have'} no result. The gate does not prove complete coverage.</span></div>}
                    <div className="result-list">{run!.scenarios.map((scenario, index) => {
                      const result = results.get(scenario.id)
                      return <details className="result-card" key={scenario.id}><summary><span className="result-index">{String(index + 1).padStart(2, '0')}</span><span className="result-title"><strong>{scenario.name}</strong><small>{scenario.expected_outcome}</small></span>{result ? <Status value={result.status} /> : <Status value="pending" />}<ChevronDown size={16} className="summary-chevron" /></summary><div className="result-body"><div className="release-expected"><span>Expected outcome</span><p>{scenario.expected_outcome}</p></div>{result ? <Result result={result} /> : <p className="muted">No result was recorded for this scenario.</p>}</div></details>
                    })}</div>
                  </section>
                </>}
          </section></div>}
  </>
}
