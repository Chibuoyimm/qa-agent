import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertCircle, Check, Code2, FileText, GitBranch, LoaderCircle, LockKeyhole, Plus, RefreshCw, ShieldCheck } from 'lucide-react'
import { api, formatDate, type RepositoryProvider, type RepositoryRole, type RepositorySnapshot, type RepositorySnapshotSummary } from './api'

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
    {loadingList ? <div className="repository-loading"><LoaderCircle className="spin" size={17} />Loading snapshots…</div> : snapshots.length === 0 ? <div className="repository-empty">No snapshots in this project yet. Import exact files to add repository context.</div> : <div className="repository-list">{snapshots.map(snapshot => <article ref={snapshot.id === focusSnapshotId ? focusedArticle : undefined} className={`repository-item ${selected.includes(snapshot.id) ? 'selected' : ''}`} key={snapshot.id}>
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

export default function RepositorySnapshots({ token, projectId, focusSnapshotId = '' }: { token: string; projectId: string; focusSnapshotId?: string }) {
  const [provider, setProvider] = useState<RepositoryProvider>('github')
  const [repository, setRepository] = useState('')
  const [ref, setRef] = useState('main')
  const [role, setRole] = useState<RepositoryRole>('frontend')
  const [paths, setPaths] = useState('')
  const [repositoryToken, setRepositoryToken] = useState('')
  const [syncing, setSyncing] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [refreshKey, setRefreshKey] = useState(0)
  const controller = useRef<AbortController | null>(null)
  const generation = useRef(0)

  useEffect(() => () => { generation.current += 1; controller.current?.abort(); setRepositoryToken('') }, [token, projectId])

  async function sync(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setError(''); setSuccess('')
    const transientToken = repositoryToken.trim(); setRepositoryToken('')
    const exactPaths = paths.split(/\r?\n/).map(path => path.trim())
    if (provider === 'github' && !/^[^/\s]+\/[^/\s]+$/.test(repository.trim())) { setError('Enter a GitHub owner/name repository.'); return }
    if (provider === 'azure' && !/^https:\/\/dev\.azure\.com\/[^/]+\/[^/]+\/_git\/[^/?#]+$/.test(repository.trim())) { setError('Enter an Azure URL: https://dev.azure.com/organization/project/_git/repository.'); return }
    if (!ref.trim()) { setError('Enter a branch, tag, or commit ref.'); return }
    if (exactPaths.length < 1 || exactPaths.length > 20 || exactPaths.some(path => !path || path.startsWith('/') || path.includes('\\') || path.split('/').some(part => part === '.' || part === '..'))) { setError('Enter 1–20 exact repository-relative file paths, one per line.'); return }
    if (new Set(exactPaths).size !== exactPaths.length) { setError('Remove duplicate file paths.'); return }
    const abort = new AbortController(); controller.current = abort
    const current = ++generation.current
    setSyncing(true)
    try {
      const result = await api<RepositorySnapshot>(token, `/api/projects/${encodeURIComponent(projectId)}/repositories/sync`, {
        method: 'POST', signal: abort.signal,
        headers: transientToken ? { [provider === 'azure' ? 'X-QA-Azure-PAT' : 'X-QA-GitHub-Token']: transientToken } : undefined,
        body: JSON.stringify({ provider, repository: repository.trim(), ref: ref.trim(), role, paths: exactPaths }),
      })
      if (current === generation.current && !abort.signal.aborted) { setSuccess(`Saved ${result.repository} at ${result.commit_sha.slice(0, 12)}. Review the imported files below.`); setRefreshKey(value => value + 1) }
    } catch (err) { if (current === generation.current && !abort.signal.aborted) setError(message(err)) }
    finally { if (current === generation.current) { setSyncing(false); controller.current = null } }
  }

  function cancel() { generation.current += 1; controller.current?.abort(); controller.current = null; setRepositoryToken(''); setSyncing(false); setError('Import cancelled.') }

  return <><div className="page-heading"><div><div className="eyebrow">REPOSITORY CONTEXT</div><h1>Repository snapshots</h1><p>Import selected source files as fixed, reviewable context for AI proposals.</p></div></div>
    <div className="repository-layout"><form className="panel repository-sync" onSubmit={event => void sync(event)}><div className="panel-kicker">IMPORT SOURCE</div><h2>Choose exact files</h2><p className="field-help">Only the paths you list will be fetched. Imports do not execute repository code.</p>
      <label className="field-label" htmlFor="repository-provider">Repository provider</label><select id="repository-provider" value={provider} disabled={syncing} onChange={event => { setProvider(event.target.value as RepositoryProvider); setRepositoryToken(''); setRepository(''); setError(''); setSuccess('') }}><option value="github">GitHub</option><option value="azure">Azure Repos</option></select>
      <label className="field-label" htmlFor="repository-name">{provider === 'azure' ? 'Azure repository URL' : 'GitHub repository'} <span>*</span></label><input id="repository-name" value={repository} placeholder={provider === 'azure' ? 'https://dev.azure.com/org/project/_git/repo' : 'owner/name'} autoComplete="off" onChange={event => setRepository(event.target.value)} />
      <div className="repository-form-row"><div><label className="field-label" htmlFor="repository-ref">Ref <span>*</span></label><input id="repository-ref" value={ref} placeholder="main" onChange={event => setRef(event.target.value)} /></div><div><label className="field-label" htmlFor="repository-role">Role <span>*</span></label><select id="repository-role" value={role} onChange={event => setRole(event.target.value as RepositoryRole)}><option value="frontend">Frontend</option><option value="backend">Backend</option></select></div></div>
      {provider === 'azure' && <p className="field-help">Azure DevOps Services only. Use a branch name, refs/tags/name for a tag, or a full commit SHA. Project and repository names may contain spaces.</p>}
      <label className="field-label" htmlFor="repository-paths">Exact file paths <span>*</span></label><textarea id="repository-paths" rows={7} value={paths} placeholder={'src/App.tsx\nsrc/api.ts'} onChange={event => setPaths(event.target.value)} /><p className="field-help">One repository-relative text file per line, up to 20. Total content limit: 40,000 bytes.</p>
      <label className="field-label" htmlFor="repository-token">{provider === 'azure' ? 'Azure personal access token' : 'GitHub token'} <span className="optional">optional</span></label><div className="repository-token-field"><LockKeyhole size={16} /><input id="repository-token" type="password" autoComplete="off" value={repositoryToken} placeholder={provider === 'azure' ? 'Needed when Azure denies anonymous access' : 'Only needed for a private repository'} onChange={event => setRepositoryToken(event.target.value)} /></div><p className="field-help">{provider === 'azure' ? 'Use a token with Code (Read) permission. ' : ''}Used for this import only. Cleared on submit, cancel, project change, or disconnect.</p>
      {error && <div className="error-banner" role="alert"><AlertCircle size={16} /><span>{error}</span></div>}{success && <div className="repository-success" role="status"><Check size={16} />{success}</div>}
      <div className="repository-sync-actions">{syncing ? <button className="button button-danger" type="button" onClick={cancel}>Cancel import</button> : <button className="button button-primary" type="submit"><Code2 size={16} />Sync snapshot</button>}</div>
    </form><aside className="repository-explainer"><ShieldCheck size={20} /><div><strong>Review before sharing</strong><p>Snapshots are immutable and pinned to a Git commit. Open the complete files below before selecting them for a proposal. Human requirements remain the source of truth for expected outcomes.</p></div></aside></div>
    <SnapshotBrowser token={token} projectId={projectId} refreshKey={refreshKey} focusSnapshotId={focusSnapshotId} />
  </>
}
