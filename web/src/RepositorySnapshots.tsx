import { useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertCircle, Check, Code2, LockKeyhole, ShieldCheck } from 'lucide-react'
import { api, type RepositoryProvider, type RepositoryRole, type RepositorySnapshot } from './api'

import { SnapshotBrowser } from './SnapshotBrowser'
import AiProposals from './AiProposals'

function message(error: unknown): string { return error instanceof Error ? error.message : 'The request failed.' }

export default function RepositorySnapshots({ token, projectId, focusSnapshotId = '' }: { token: string; projectId: string; focusSnapshotId?: string }) {
  const [provider, setProvider] = useState<RepositoryProvider>('github')
  const [repository, setRepository] = useState('')
  const [ref, setRef] = useState('main')
  const [role, setRole] = useState<RepositoryRole>('frontend')
  const [mode, setMode] = useState<'agent' | 'manual'>('agent')
  const [foundId, setFoundId] = useState('')
  const [paths, setPaths] = useState('')
  const [repositoryToken, setRepositoryToken] = useState('')
  const [syncing, setSyncing] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [refreshKey, setRefreshKey] = useState(0)
  const controller = useRef<AbortController | null>(null)
  const generation = useRef(0)

  useEffect(() => () => { generation.current += 1; controller.current?.abort(); setRepositoryToken('') }, [token, projectId])

  async function search(options: RequestInit) {
    const transientToken = repositoryToken.trim(); setRepositoryToken(''); setError(''); setSuccess('')
    const result = await api<{ snapshot: RepositorySnapshot | null; reason: string; candidates: number; excluded: number }>(token, `/api/projects/${encodeURIComponent(projectId)}/repositories/search`, {
      ...options, headers: { ...options.headers, ...(transientToken ? { [provider === 'azure' ? 'X-QA-Azure-PAT' : 'X-QA-GitHub-Token']: transientToken } : {}) },
      body: JSON.stringify({ repository: { provider, repository: repository.trim(), ref: ref.trim(), role }, ai: JSON.parse(String(options.body)) }),
    })
    if (options.signal?.aborted) return
    setSuccess(`${result.reason} Searched ${result.candidates} eligible files; excluded ${result.excluded} entries. ${result.snapshot ? 'Review the imported files below before sharing their contents.' : 'No files imported.'}`)
    if (result.snapshot) { setFoundId(result.snapshot.id); setRefreshKey(value => value + 1) }
  }

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
    <div className="repository-layout"><div className="panel repository-sync"><div className="panel-kicker">IMPORT SOURCE</div><h2>Find files for your checks</h2><p className="field-help">Describe what to test and let the agent choose relevant source files. Imports do not execute repository code.</p>
      <label className="field-label" htmlFor="repository-mode">File selection</label><select id="repository-mode" value={mode} disabled={syncing} onChange={event => { setMode(event.target.value as 'agent' | 'manual'); setRepositoryToken(''); setError(''); setSuccess('') }}><option value="agent">Let the agent find files</option><option value="manual">Choose exact files manually</option></select>
      <form onSubmit={event => { if (mode === 'manual') void sync(event); else event.preventDefault() }}>
      <label className="field-label" htmlFor="repository-provider">Repository provider</label><select id="repository-provider" value={provider} disabled={syncing} onChange={event => { setProvider(event.target.value as RepositoryProvider); setRepositoryToken(''); setRepository(''); setError(''); setSuccess('') }}><option value="github">GitHub</option><option value="azure">Azure Repos</option></select>
      <label className="field-label" htmlFor="repository-name">{provider === 'azure' ? 'Azure repository URL' : 'GitHub repository'} <span>*</span></label><input id="repository-name" disabled={syncing} value={repository} placeholder={provider === 'azure' ? 'https://dev.azure.com/org/project/_git/repo' : 'owner/name'} autoComplete="off" onChange={event => setRepository(event.target.value)} />
      <div className="repository-form-row"><div><label className="field-label" htmlFor="repository-ref">Ref <span>*</span></label><input id="repository-ref" disabled={syncing} value={ref} placeholder="main" onChange={event => setRef(event.target.value)} /></div><div><label className="field-label" htmlFor="repository-role">Role <span>*</span></label><select id="repository-role" disabled={syncing} value={role} onChange={event => setRole(event.target.value as RepositoryRole)}><option value="frontend">Frontend</option><option value="backend">Backend</option></select></div></div>
      {provider === 'azure' && <p className="field-help">Azure DevOps Services only. Use a branch name, refs/tags/name for a tag, or a full commit SHA. Project and repository names may contain spaces.</p>}
      {mode === 'manual' && <><label className="field-label" htmlFor="repository-paths">Exact file paths <span>*</span></label><textarea id="repository-paths" rows={7} value={paths} placeholder={'src/App.tsx\nsrc/api.ts'} onChange={event => setPaths(event.target.value)} /><p className="field-help">One repository-relative text file per line, up to 20. Total content limit: 40,000 bytes.</p></>}
      <label className="field-label" htmlFor="repository-token">{provider === 'azure' ? 'Azure personal access token' : 'GitHub token'} <span className="optional">optional</span></label><div className="repository-token-field"><LockKeyhole size={16} /><input id="repository-token" disabled={syncing} type="password" autoComplete="off" value={repositoryToken} placeholder={provider === 'azure' ? 'Needed when Azure denies anonymous access' : 'Only needed for a private repository'} onChange={event => setRepositoryToken(event.target.value)} /></div><p className="field-help">{provider === 'azure' ? 'Use a token with Code (Read) permission. ' : ''}Used for this import only. Cleared on submit, cancel, project change, or disconnect.</p>
      {error && <div className="error-banner" role="alert"><AlertCircle size={16} /><span>{error}</span></div>}{success && <div className="repository-success" role="status"><Check size={16} />{success}</div>}
      {mode === 'manual' && <div className="repository-sync-actions">{syncing ? <button className="button button-danger" type="button" onClick={cancel}>Cancel import</button> : <button className="button button-primary" type="submit"><Code2 size={16} />Sync snapshot</button>}</div>}
      </form>
      {mode === 'agent' && <AiProposals key={`${projectId}:${provider}`} token={token} projectId={projectId} onClose={() => { setMode('manual'); setRepositoryToken('') }} onReview={() => {}} repositorySearch={{ identity: `${repository}:${ref}:${role}`, ready: Boolean(repository.trim() && ref.trim()), send: search, onBusy: setSyncing }} />}
    </div><aside className="repository-explainer"><ShieldCheck size={20} /><div><strong>Review before sharing</strong><p>Snapshots are immutable and pinned to a Git commit. Open the complete files below before selecting them for a proposal. Human requirements remain the source of truth for expected outcomes.</p></div></aside></div>
    <SnapshotBrowser token={token} projectId={projectId} refreshKey={refreshKey} focusSnapshotId={foundId || focusSnapshotId} />
  </>
}
