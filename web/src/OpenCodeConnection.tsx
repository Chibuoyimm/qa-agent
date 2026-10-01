import { useEffect, useRef, useState } from 'react'
import { AlertCircle, Check, ExternalLink, LoaderCircle, LockKeyhole, RefreshCw } from 'lucide-react'
import { api, type OpenCodeStatus } from './api'

export function OpenCodeConnection({ token, disabled, onStatus, onBusy }: {
  token: string
  disabled: boolean
  onStatus: (status: OpenCodeStatus | null) => void
  onBusy: (busy: boolean) => void
}) {
  const [status, setStatus] = useState<OpenCodeStatus | null>(null)
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const controller = useRef<AbortController | null>(null)
  const callbacks = useRef({ onStatus, onBusy })
  callbacks.current = { onStatus, onBusy }

  async function update(action: 'status' | 'connect' | 'disconnect') {
    controller.current?.abort()
    const abort = new AbortController()
    controller.current = abort
    const connectionKey = key.trim()
    setKey('')
    setBusy(true)
    setError('')
    callbacks.current.onBusy(true)
    callbacks.current.onStatus(null)
    try {
      const value = await api<OpenCodeStatus>(token, `/api/ai/opencode${action === 'status' ? '' : `/${action}`}`, {
        signal: abort.signal,
        ...(action === 'status' ? {} : { method: 'POST', ...(action === 'connect' ? { body: JSON.stringify({ key: connectionKey }) } : {}) }),
      })
      if (!abort.signal.aborted) { setStatus(value); callbacks.current.onStatus(value) }
    } catch (cause) {
      if (!abort.signal.aborted) { setStatus(null); setError(cause instanceof Error ? cause.message : 'Could not check OpenCode Go.') }
    } finally {
      if (!abort.signal.aborted) { setBusy(false); callbacks.current.onBusy(false) }
    }
  }

  useEffect(() => {
    void update('status')
    return () => { controller.current?.abort(); callbacks.current.onBusy(false) }
    // The provider panel remounts on token changes; callbacks use their latest values.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  return <div className="ai-chatgpt">
    <p>Use your OpenCode Go or Go Plus subscription. Connect its key once; OpenCode stores it in this app’s protected local connection.</p>
    <div className="ai-chatgpt-actions">
      {busy && <span><LoaderCircle size={14} className="spin" />Checking connection…</span>}
      {status?.connected && <><span className="ai-connected"><Check size={14} />Key connected</span><button type="button" className="link-button" disabled={busy || disabled} onClick={() => void update('disconnect')}>Disconnect OpenCode Go</button></>}
      <button type="button" className="link-button" disabled={busy || disabled} onClick={() => void update('status')}><RefreshCw size={13} />Refresh OpenCode</button>
    </div>
    {!status?.connected && <><label className="field-label" htmlFor="ai-opencode-key">OpenCode Go subscription key</label><input id="ai-opencode-key" type="password" autoComplete="off" value={key} disabled={busy || disabled} placeholder="Enter your Go subscription key" onChange={event => setKey(event.target.value)} /><p className="ai-key-note"><LockKeyhole size={13} />Cleared on connect. Never saved in browser storage.</p><button type="button" className="button" disabled={!key.trim() || busy || disabled} onClick={() => void update('connect')}>Connect OpenCode Go</button></>}
    <p className="ai-workspace-note">For subscription-only usage, turn off “Use balance” in your OpenCode console. With it enabled, OpenCode can spend Zen credits after your plan limit. A connected key does not verify your plan or remaining usage.</p>
    {status?.connected && !busy && status.models.length === 0 && <p className="ai-key-note">No compatible Go models are available. Refresh after checking your subscription.</p>}
    {error && <div className="error-banner"><AlertCircle size={16} /><span>{error}</span></div>}
    <a href="https://opencode.ai/auth" target="_blank" rel="noopener noreferrer">OpenCode console <ExternalLink size={12} /></a>
  </div>
}
