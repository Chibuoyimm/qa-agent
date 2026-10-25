import { useEffect, useRef, useState } from 'react'
import { api, formatDate, type Discovery, type Scenario } from './api'
import { errorText } from './workspace'
export type VerifiedAccess = { discovery_id: string; setup_scenario_id: string; verified_at?: string }

export default function AppReadiness({ token, projectId, scenarios, access, aiReady, onVerified, onAI, onAddAccess, onTest }: {
  token: string; projectId: string; scenarios: Scenario[]; access: VerifiedAccess | null; aiReady: boolean
  onVerified: (access: VerifiedAccess) => void; onAI: () => void; onAddAccess: () => void; onTest: () => void
}) {
  const [setup, setSetup] = useState(access?.setup_scenario_id ?? '')
  const [discovery, setDiscovery] = useState<Discovery | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const request = useRef<AbortController | null>(null)
  const verifiedCallback = useRef(onVerified)
  verifiedCallback.current = onVerified
  useEffect(() => setSetup(access?.setup_scenario_id ?? ''), [access?.setup_scenario_id])
  useEffect(() => () => request.current?.abort(), [])
  useEffect(() => {
    if (!discovery || !['queued', 'running'].includes(discovery.status)) return
    const abort = new AbortController()
    const timer = setTimeout(async () => {
      try {
        const value = await api<Discovery>(token, `/api/discoveries/${discovery.id}`, {signal: abort.signal})
        if (value.status === 'completed') {
          const saved = await api<VerifiedAccess>(token, `/api/projects/${projectId}/access`, {method: 'POST', signal: abort.signal, body: JSON.stringify({discovery_id: value.id})})
          if (!abort.signal.aborted) verifiedCallback.current(saved)
        }
        if (!abort.signal.aborted) { setDiscovery(value); if (value.status === 'error') setError(value.error || 'Browser access could not be verified.'); setBusy(['queued','running'].includes(value.status)) }
      } catch (err) { if (!abort.signal.aborted) {setError(errorText(err));setBusy(false)} }
    }, 1500)
    return () => { clearTimeout(timer); abort.abort() }
  }, [discovery, token, projectId])
  async function verify() {
    request.current?.abort(); const abort = new AbortController(); request.current = abort
    setBusy(true);setError('')
    try { setDiscovery(await api<Discovery>(token, `/api/projects/${projectId}/discoveries`, {method:'POST',signal:abort.signal,body:JSON.stringify({start_path:'/',max_pages:1,...(setup?{setup_scenario_id:setup}:{})})})) }
    catch (err) {if (!abort.signal.aborted) {setError(errorText(err));setBusy(false)}}
  }
  const matches = Boolean(access?.discovery_id && setup === access.setup_scenario_id && !error)
  return <section className="flow-card app-readiness"><div className="eyebrow">GET READY TO TEST</div><h2>{matches && aiReady ? 'Ready to plan checks' : 'Finish your app setup'}</h2>
    <ol className="readiness-list">
      <li><strong>1. App URL registered</strong><span>Connected to this workspace</span></li>
      <li><strong>2. Browser access {matches ? 'verified' : busy ? 'being checked…' : 'needs verification'}</strong><span>{matches ? `Last verified ${formatDate(access?.verified_at)}` : 'Check that the browser worker can open the app.'}</span></li>
      <li><strong>3. {setup ? 'Private app access' : 'Public app access'}</strong><span>{setup ? matches ? 'Saved access steps succeeded; reused for new requests.' : 'Verify an approved sign-in check before saving it as the default.' : 'No sign-in steps selected. Choose a setup below for a private app.'}</span></li>
      <li><strong>4. AI {aiReady ? 'ready' : 'needs a connection or model'}</strong><button className="link-button" onClick={onAI}>{aiReady ? 'Manage AI connection' : 'Connect AI and choose a model'}</button></li>
    </ol>
    <label>Access for this app<select value={setup} disabled={busy} onChange={event => {setSetup(event.target.value);setError('')}}><option value="">Public app — no sign-in</option>{scenarios.filter(s=>s.approved&&s.steps.every(step=>['navigate','fill','click','assert_text','assert_visible'].includes(step.action))).map(s=><option key={s.id} value={s.id}>{s.name}</option>)}</select></label>
    <p>For a private app, use an access check that signs in and confirms a private page is visible. An engineer configures the saved test credentials.</p>
    <div className="flow-row"><button className="button button-outline" onClick={onAddAccess}>Set up private app sign-in</button><button className="button button-primary" disabled={busy} onClick={()=>void verify()}>{busy ? 'Verifying browser access…' : 'Verify access & save default'}</button></div>
    {error && <p role="alert" className="error-banner">{error}</p>}
    {matches && <p>Access verification is evidence that these pages opened at the recorded time. It does not prove the app’s features work.</p>}
    {matches && aiReady && <button className="button button-primary" onClick={onTest}>Start a test request</button>}
  </section>
}
