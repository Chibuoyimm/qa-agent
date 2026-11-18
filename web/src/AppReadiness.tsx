import { useEffect, useState } from 'react'
import { api, formatDate, type Scenario, type ScenarioInput } from './api'
import { errorText, stepLabel } from './workspace'

export type VerifiedAccess = {
  discovery_id: string
  setup_scenario_id: string
  verified_at?: string
  verification_status?: string
  verification_error?: string
  requested_setup?: string
}
export type AIReadiness = 'ready' | 'per_request' | 'unavailable'
type Props = {
  token: string
  projectId: string
  projectName: string
  handoffURL: string
  target: string
  scenarios: Scenario[]
  access: VerifiedAccess | null
  aiReady: AIReadiness
  onVerificationStarted: () => void
  onAI: () => void
  onTest: () => void
}
export default function AppReadiness({ token, projectId, projectName, target, handoffURL, scenarios, access, aiReady, onVerificationStarted, onAI, onTest }: Props) {
  const savedSetup = (access?.verification_status ? access.requested_setup : access?.setup_scenario_id) || ''
  const [setup, setSetup] = useState(savedSetup)
  const [error, setError] = useState('')
  const [starting, setStarting] = useState(false)
  const [privateSetup, setPrivateSetup] = useState(false)
  const [reviewSignIn, setReviewSignIn] = useState(false)
  const [savingSignIn, setSavingSignIn] = useState(false)
  const [savedSignIn, setSavedSignIn] = useState<Scenario | null>(null)
  const [copied,setCopied] = useState(false)
  const [path,setPath] = useState('/login')
  const [email,setEmail] = useState('Email')
  const [password,setPassword] = useState('Password')
  const [submit,setSubmit] = useState('Sign in')
  const [heading,setHeading] = useState('')
  useEffect(() => setSetup(savedSetup), [savedSetup])
  useEffect(onVerificationStarted,[onVerificationStarted])
  const pending = starting || ['queued','running'].includes(access?.verification_status || '')
  const matches = Boolean(access?.discovery_id && setup === access.setup_scenario_id)
  const handoff = `Please set up private browser sign-in for ${projectName} at ${target}. Configure the worker's QA_TEST_EMAIL and QA_TEST_PASSWORD for a test account, review the sign-in steps and verify access here: ${handoffURL}`
  async function verify() {
    setStarting(true)
    setError('')
    try {
      await api(token, `/api/projects/${projectId}/discoveries`, {method:'POST', body:JSON.stringify({start_path:'/',max_pages:1,setup_scenario_id:setup,save_access_default:true})})
      onVerificationStarted()
    } catch (err) {setError(errorText(err))}
    finally {setStarting(false)}
  }
  function signInDraft(): ScenarioInput {
    return {
      name: `Sign in to ${projectName}`, description:'Open the private app with the configured test account.',
      expected_outcome:`After sign-in, the heading “${heading}” is visible.`, approved:false,
      steps:[
        {action:'navigate',path}, {action:'fill',locator:{label:email},secret_env:'QA_TEST_EMAIL'},
        {action:'fill',locator:{label:password},secret_env:'QA_TEST_PASSWORD'},
        {action:'click',locator:{role:'button',name:submit}}, {action:'assert_visible',locator:{role:'heading',name:heading}},
      ],
    }
  }
  async function saveSignIn() {
    setSavingSignIn(true)
    setError('')
    try {
      const saved = await api<Scenario>(token, `/api/projects/${projectId}/scenarios`, {
        method: 'POST', body: JSON.stringify({...signInDraft(), approved: true}),
      })
      setSavedSignIn(saved)
      setSetup(saved.id)
      setPrivateSetup(false)
      setReviewSignIn(false)
      onVerificationStarted()
    } catch (err) { setError(errorText(err)) }
    finally { setSavingSignIn(false) }
  }
  const choices = savedSignIn && !scenarios.some(s => s.id === savedSignIn.id) ? [...scenarios, savedSignIn] : scenarios
  return <section className="flow-card app-readiness">
    <h2>{matches ? 'Browser access verified' : 'Connect the test browser'}</h2>
    <p>{projectName} · {target}</p>
    <label>Access for this app<select value={setup} disabled={pending} onChange={e => {setSetup(e.target.value);setError('')}}>
      <option value="">Public app — no sign-in</option>
      {choices.filter(s => s.approved && s.steps.every(step => ['navigate','fill','click','assert_text','assert_visible'].includes(step.action))).map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
    </select></label>
    <div className="flow-row"><button className="button button-outline" aria-expanded={privateSetup} onClick={() => setPrivateSetup(!privateSetup)}>Set up private app sign-in</button>
      <button className="button button-primary" disabled={pending} onClick={() => void verify()}>{pending ? 'Verifying browser access…' : 'Verify access & save default'}</button></div>
    {pending && <p role="status">The browser is checking access. You can leave this page and return to the result.</p>}
    {savedSignIn?.id === setup && !matches && !pending && <p role="status">Sign-in check selected. Verify browser access to use it on new requests.</p>}
    {matches && <p role="status">Browser access verified · {formatDate(access?.verified_at)}</p>}
    {(error || access?.verification_error) && <p className="error-banner" role="alert">{error || access?.verification_error}</p>}
    {privateSetup && <section className="access-guide"><h3>Sign-in required</h3>
      <p>Use the labels shown on the sign-in page. An engineer can supply the test account on the worker.</p>
      <fieldset disabled={savingSignIn} onChange={() => setReviewSignIn(false)}><label>Sign-in page path<input value={path} onChange={e => setPath(e.target.value)}/></label>
      <label>Email field label<input value={email} onChange={e=>setEmail(e.target.value)}/></label>
      <label>Password field label<input value={password} onChange={e=>setPassword(e.target.value)}/></label>
      <label>Sign-in button name<input value={submit} onChange={e=>setSubmit(e.target.value)}/></label>
      <label>Heading visible after sign-in<input value={heading} placeholder="e.g. Your dashboard" onChange={e=>setHeading(e.target.value)}/></label></fieldset>
      <button className="button button-outline" disabled={![path,email,password,submit,heading].every(v=>v.trim())} onClick={() => setReviewSignIn(true)}>Review sign-in check</button>
      {reviewSignIn && <div className="sign-in-review"><h4>What the browser will do</h4>
        <ol>{signInDraft().steps.map((step, i) => <li key={i}>{stepLabel(step)}</li>)}</ol>
        <button className="button button-primary" disabled={savingSignIn} onClick={() => void saveSignIn()}>{savingSignIn ? 'Saving…' : 'Approve & select sign-in check'}</button>
      </div>}
      <details><summary>Share setup with an engineer</summary><p>{handoff}</p>
        <button className="link-button" onClick={async()=>{try{await navigator.clipboard.writeText(handoff);setCopied(true)}catch{setError('Select and copy the setup request above.')}}}>{copied ? 'Copied' : 'Copy setup request'}</button></details>
    </section>}
    <div className="readiness-next"><strong>{aiReady === 'per_request' ? 'AI key required per request' : aiReady === 'ready' ? 'AI ready' : 'Choose an AI connection'}</strong>
      <button className="link-button" onClick={onAI}>Manage AI connection</button></div>
    <button className="button button-outline" onClick={onTest}>Return to test request</button>
  </section>
}
