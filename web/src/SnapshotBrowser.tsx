import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertCircle, Check, FileText, GitBranch, LoaderCircle, Plus, RefreshCw, ShieldCheck } from 'lucide-react'
import { api, formatDate, type RepositorySnapshot, type RepositorySnapshotSummary } from './api'
function message(error: unknown): string { return error instanceof Error ? error.message : 'The request failed.' }
export function SnapshotBrowser({ token, projectId, selected = [], onSelectionChange, refreshKey = 0, focusSnapshotId = '' }: {
  token: string
  projectId: string
  selected?: string[]
  onSelectionChange?: (ids: string[]) => void
  refreshKey?: number
  focusSnapshotId?: string
}) {
  const [snapshots, setSnapshots] = useState<RepositorySnapshotSummary[]>([])
  const [details, setDetails] = useState<Record<string, RepositorySnapshot>>({})
  const [openIds, setOpenIds] = useState<string[]>([])
  const [loadingList, setLoadingList] = useState(true)
  const [loadingDetail, setLoadingDetail] = useState<string[]>([])
  const [error, setError] = useState('')
  const requests = useRef<Map<string, AbortController>>(new Map())
  const [reload, setReload] = useState(0)
  const openedFocus = useRef('')
  const focusedArticle = useRef<HTMLElement | null>(null)

  useEffect(() => {
    const abort = new AbortController()
    setSnapshots([]); setDetails({}); setOpenIds([]); setLoadingDetail([]); setError(''); setLoadingList(true)
    void api<RepositorySnapshotSummary[]>(token, `/api/projects/${encodeURIComponent(projectId)}/repositories`, { signal: abort.signal })
      .then(async items => {
        if (abort.signal.aborted) return
        if (focusSnapshotId && !items.some(item => item.id === focusSnapshotId)) {
          try {
            const focused = await api<RepositorySnapshot>(token, `/api/projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(focusSnapshotId)}`, { signal: abort.signal })
            if (abort.signal.aborted) return
            items = [focused, ...items]
            setDetails(previous => ({ ...previous, [focused.id]: focused }))
          } catch (cause) {
            if (abort.signal.aborted) return
            setError(`Could not load the linked snapshot: ${message(cause)}`)
          }
        }
        openedFocus.current = ''
        setSnapshots(items)
      })
      .catch(err => { if (!abort.signal.aborted) setError(`Could not load repository snapshots: ${message(err)}`) })
      .finally(() => { if (!abort.signal.aborted) setLoadingList(false) })
    return () => { abort.abort(); requests.current.forEach(controller => controller.abort()); requests.current.clear() }
  }, [token, projectId, refreshKey, reload, focusSnapshotId])

  const open = useCallback(async (id: string) => {
    if (openIds.includes(id) && details[id]) { if (!selected.includes(id)) setOpenIds(current => current.filter(item => item !== id)); return }
    if (loadingDetail.includes(id)) return
    setError(''); setOpenIds(current => current.includes(id) ? current : [...current, id])
    if (details[id]) return
    const abort = new AbortController()
    requests.current.set(id, abort); setLoadingDetail(current => [...current, id])
    try {
      const result = await api<RepositorySnapshot>(token, `/api/projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(id)}`, { signal: abort.signal })
      if (!abort.signal.aborted) setDetails(previous => ({ ...previous, [id]: result }))
    } catch (err) {
      if (!abort.signal.aborted) setError(`Could not load snapshot files: ${message(err)}`)
    } finally { if (requests.current.get(id) === abort) requests.current.delete(id); if (!abort.signal.aborted) setLoadingDetail(current => current.filter(item => item !== id)) }
  }, [token, projectId, openIds, selected, details, loadingDetail])

  useEffect(() => {
    if (loadingList || !focusSnapshotId || openedFocus.current === focusSnapshotId || !snapshots.some(item => item.id === focusSnapshotId)) return
    openedFocus.current = focusSnapshotId
    void open(focusSnapshotId)
  }, [focusSnapshotId, snapshots, loadingList, open])

  useEffect(() => {
    if (focusSnapshotId && openIds.includes(focusSnapshotId)) focusedArticle.current?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }, [focusSnapshotId, openIds])

  function toggle(snapshot: RepositorySnapshotSummary) {
    if (!onSelectionChange || !details[snapshot.id]) return
    if (selected.includes(snapshot.id)) { onSelectionChange(selected.filter(id => id !== snapshot.id)); return }
    if (selected.some(id => snapshots.find(item => item.id === id)?.role === snapshot.role)) {
      setError(`Only one ${snapshot.role} snapshot can be selected. Clear the current one first.`); return
    }
    if (selected.length >= 2) { setError('Select at most two snapshots.'); return }
    setError(''); onSelectionChange([...selected, snapshot.id])
  }

  return <div className="repository-browser">
    <div className="repository-browser-head"><div><h3>Saved snapshots</h3><p>Each snapshot is pinned to a commit. Open it to inspect every included file.</p></div><button type="button" className="button button-outline" onClick={() => { if (selected.length) onSelectionChange?.([]); setReload(value => value + 1) }} disabled={loadingList}><RefreshCw size={15} />Refresh</button></div>
    {error && <div className="error-banner" role="alert"><AlertCircle size={16} /><span>{error}</span></div>}
    {loadingList ? <div className="repository-loading"><LoaderCircle className="spin" size={17} />Loading snapshots…</div> : snapshots.length === 0 ? <div className="repository-empty">No snapshots in this project yet. Find relevant files or import selected files to add repository context.</div> : <div className="repository-list">{snapshots.map(snapshot => <article ref={snapshot.id === focusSnapshotId ? focusedArticle : undefined} className={`repository-item ${selected.includes(snapshot.id) ? 'selected' : ''}`} key={snapshot.id}>
      <div className="repository-item-top"><div><span className="repository-role">{snapshot.provider === 'azure' ? 'Azure Repos' : 'GitHub'} · {snapshot.role}</span><h4>{snapshot.repository}</h4><p><GitBranch size={13} />{snapshot.ref} · <code title={snapshot.commit_sha}>{snapshot.commit_sha.slice(0, 12)}</code> · {formatDate(snapshot.created_at)}</p></div><button type="button" className="link-button" aria-expanded={openIds.includes(snapshot.id)} onClick={() => void open(snapshot.id)}>{openIds.includes(snapshot.id) ? details[snapshot.id] ? selected.includes(snapshot.id) ? 'Files open for review' : 'Hide files' : loadingDetail.includes(snapshot.id) ? 'Loading files…' : 'Retry files' : 'Review files'}</button></div>
      <div className="repository-item-meta"><span>{snapshot.file_count} {snapshot.file_count === 1 ? 'file' : 'files'}</span><span>{snapshot.total_bytes.toLocaleString()} bytes</span><span>content SHA-256 <code title={snapshot.content_sha256}>{snapshot.content_sha256.slice(0, 12)}</code></span></div>
      {openIds.includes(snapshot.id) && (loadingDetail.includes(snapshot.id) ? <div className="repository-loading"><LoaderCircle className="spin" size={16} />Loading complete file content…</div> : details[snapshot.id] && <div className="repository-files">
        <p className="repository-review-note"><ShieldCheck size={15} />Read the complete imported content below before consenting to share it with an AI provider. Source code can contain sensitive information despite import checks.</p>
        {details[snapshot.id].files.map(file => <div className="repository-file" key={file.path}><div><FileText size={14} /><code>{file.path}</code></div><pre>{file.content}</pre></div>)}
        {onSelectionChange && <button type="button" className={selected.includes(snapshot.id) ? 'button button-outline' : 'button button-primary'} onClick={() => toggle(snapshot)}>{selected.includes(snapshot.id) ? <Check size={15} /> : <Plus size={15} />}{selected.includes(snapshot.id) ? 'Selected for proposal' : 'Use this reviewed snapshot'}</button>}
      </div>)}
    </article>)}</div>}
  </div>
}
