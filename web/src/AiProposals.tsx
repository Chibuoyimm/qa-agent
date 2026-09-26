import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  AlertCircle, ArrowRight, Check, CircleHelp, Info, KeyRound, LoaderCircle,
  LockKeyhole, RefreshCw, ShieldCheck, Sparkles, Square, X,
} from 'lucide-react'
import { api, type AiConfig, type AiProposalResponse, type ScenarioInput } from './api'

type CredentialMode = 'managed' | 'byok'

export default function AiProposals({ token, projectId, onClose, onReview }: {
  token: string
  projectId: string
  onClose: () => void
  onReview: (scenario: ScenarioInput) => void
}) {
  const [config, setConfig] = useState<AiConfig | null>(null)
  const [configLoading, setConfigLoading] = useState(true)
  const [configError, setConfigError] = useState('')
  const [model, setModel] = useState('')
  const [credentialMode, setCredentialMode] = useState<CredentialMode>('managed')
  const [providerKey, setProviderKey] = useState('')
  const [prompt, setPrompt] = useState('')
  const [context, setContext] = useState('')
  const [consent, setConsent] = useState(false)
  const [response, setResponse] = useState<AiProposalResponse | null>(null)
  const [requestError, setRequestError] = useState('')
  const [generating, setGenerating] = useState(false)
  const controller = useRef<AbortController | null>(null)
  const requestNumber = useRef(0)

  useEffect(() => {
    const abort = new AbortController()
    setConfigLoading(true)
    setConfigError('')
    void api<AiConfig>(token, '/api/ai/config', { signal: abort.signal }).then(value => {
      setConfig(value)
      setModel(value.models[0] ?? '')
      setCredentialMode(value.managed_available ? 'managed' : 'byok')
    }).catch(error => {
      if (!abort.signal.aborted) setConfigError(error instanceof Error ? error.message : 'Could not load AI configuration.')
    }).finally(() => { if (!abort.signal.aborted) setConfigLoading(false) })
    return () => { abort.abort(); controller.current?.abort(); requestNumber.current += 1 }
  }, [token, projectId])

  const configured = Boolean(config && config.models.length > 0 && (config.managed_available || config.byok_available))
  const canGenerate = configured && !generating && Boolean(prompt.trim() && context.trim() && model && consent && (credentialMode === 'managed' || providerKey.trim()))

  async function generate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setRequestError('')
    if (!config || !configured) { setRequestError('AI proposal generation is not configured on this server.'); return }
    if (!config.models.includes(model)) { setRequestError('Choose a configured model.'); return }
    if (!prompt.trim() || prompt.length > 4000) { setRequestError('Testing request must be 1–4000 characters.'); return }
    if (!context.trim() || context.length > 60000) { setRequestError('App context must be 1–60000 characters.'); return }
    if (!consent) { setRequestError('Confirm consent to send these fields to OpenAI.'); return }
    if (credentialMode === 'managed' && !config.managed_available) { setRequestError('Managed access is unavailable on this server.'); return }
    if (credentialMode === 'byok' && (!config.byok_available || !providerKey.trim())) { setRequestError('Enter a provider key to use BYOK.'); return }

    const abort = new AbortController()
    controller.current = abort
    const currentRequest = ++requestNumber.current
    const transientKey = providerKey.trim()
    setProviderKey('')
    setGenerating(true)
    setResponse(null)
    try {
      const result = await api<AiProposalResponse>(token, `/api/projects/${encodeURIComponent(projectId)}/proposals`, {
        method: 'POST',
        signal: abort.signal,
        headers: credentialMode === 'byok' ? { 'X-QA-Provider-Key': transientKey } : undefined,
        body: JSON.stringify({ prompt: prompt.trim(), context: context.trim(), model, credential_mode: credentialMode, consent: true }),
      })
      if (currentRequest === requestNumber.current && !abort.signal.aborted) setResponse(result)
    } catch (error) {
      if (currentRequest === requestNumber.current && !abort.signal.aborted) setRequestError(error instanceof Error ? error.message : 'Proposal generation failed.')
    } finally {
      setProviderKey('')
      if (currentRequest === requestNumber.current) { setGenerating(false); controller.current = null }
    }
  }

  function cancel() {
    requestNumber.current += 1
    controller.current?.abort()
    controller.current = null
    setGenerating(false)
    setProviderKey('')
    setRequestError('Request cancelled. No proposals were saved.')
  }

  return <section className="ai-panel" aria-label="AI scenario proposals">
    <div className="ai-panel-head"><div className="ai-panel-icon"><Sparkles size={20} /></div><div><div className="eyebrow">AI ASSISTED DRAFTING</div><h2>Ask AI to propose checks</h2><p>Describe what you want to test and supply the relevant facts. You review every draft before it can run.</p></div><button className="icon-button" onClick={onClose} aria-label="Close AI proposals"><X size={19} /></button></div>
    <div className="ai-boundary"><Info size={16} /><span>Proposals use only the request and context you enter here. This does not inspect your repository, explore the app, or run a browser.</span></div>
    {configLoading ? <div className="ai-config-state"><LoaderCircle size={18} className="spin" />Checking model availability…</div>
      : configError ? <div className="error-banner"><AlertCircle size={17} /><span>Could not load AI configuration: {configError}</span></div>
        : !config || !configured ? <div className="ai-unavailable"><LockKeyhole size={22} /><div><strong>Proposal generation is unavailable</strong><p>{config?.models.length === 0 ? 'No model IDs are configured in QA_OPENAI_MODELS.' : 'Neither managed access nor BYOK is enabled on this server.'} Ask the operator to configure access, then reopen this panel.</p></div></div>
          : <form onSubmit={event => void generate(event)}>
            <div className="ai-form-grid"><div className="ai-main-fields"><label className="field-label" htmlFor="ai-request">Testing request <span>*</span></label><textarea id="ai-request" maxLength={4000} rows={4} placeholder="What business behaviour should be checked? What could go wrong?" value={prompt} onChange={event => { setPrompt(event.target.value); setConsent(false) }} /><div className="input-count">{prompt.length} / 4,000</div><label className="field-label" htmlFor="ai-context">Application context <span>*</span></label><textarea id="ai-context" maxLength={60000} rows={9} placeholder={'Paste relevant requirements, known paths, test IDs, fixture rules, and independent expected values.\n\nExample: Login is /login. Revenue element uses data-testid="dashboard-revenue". Paid 150000 less refund 10000; cancelled orders are excluded.'} value={context} onChange={event => { setContext(event.target.value); setConsent(false) }} /><div className="input-count">{context.length.toLocaleString()} / 60,000</div></div>
              <div className="ai-settings"><div className="ai-settings-heading"><KeyRound size={17} /><strong>Generation settings</strong></div><label className="field-label" htmlFor="ai-model">Model</label><select id="ai-model" value={model} onChange={event => { setModel(event.target.value); setConsent(false) }}>{config.models.map(item => <option key={item} value={item}>{item}</option>)}</select><div className="field-label ai-access-label">Access mode</div><div className="ai-mode-options">{config.managed_available && <label className={credentialMode === 'managed' ? 'chosen' : ''}><input type="radio" name="credential-mode" value="managed" checked={credentialMode === 'managed'} onChange={() => { setCredentialMode('managed'); setConsent(false) }} /><span><strong>Managed</strong><small>Server configured key</small></span></label>}{config.byok_available && <label className={credentialMode === 'byok' ? 'chosen' : ''}><input type="radio" name="credential-mode" value="byok" checked={credentialMode === 'byok'} onChange={() => { setCredentialMode('byok'); setConsent(false) }} /><span><strong>Bring your own key</strong><small>For this request only</small></span></label>}</div>{credentialMode === 'byok' && <><label className="field-label" htmlFor="ai-provider-key">OpenAI API key</label><input id="ai-provider-key" type="password" autoComplete="off" placeholder="Enter key for this request" value={providerKey} onChange={event => setProviderKey(event.target.value)} /><p className="ai-key-note"><LockKeyhole size={13} />Cleared on submit. Never stored in browser storage.</p></>}</div></div>
            <label className="ai-consent"><input type="checkbox" checked={consent} onChange={event => setConsent(event.target.checked)} /><span>I agree to send my testing request and app context above to OpenAI to generate proposals.</span></label>
            {requestError && <div className="error-banner"><AlertCircle size={17} /><span>{requestError}</span></div>}
            <div className="ai-actions"><span><ShieldCheck size={16} />Drafts remain unapproved and unsaved until you review them.</span>{generating ? <button className="button button-danger" type="button" onClick={cancel}><Square size={14} />Cancel request</button> : <button className="button button-primary" type="submit" disabled={!canGenerate}><Sparkles size={16} />Generate proposals</button>}</div>
          </form>}
    {response && <div className="ai-results"><div className="ai-results-head"><div><div className="eyebrow">PROPOSAL REVIEW</div><h3>{response.scenarios.length ? `${response.scenarios.length} draft ${response.scenarios.length === 1 ? 'scenario' : 'scenarios'}` : 'More context needed'}</h3><p>Generated with {response.model}. Review assertions against your own requirements before saving.</p></div><span className="ai-provider-badge"><Sparkles size={14} /> OpenAI</span></div><div className="ai-results-grid"><div className="ai-drafts">{response.scenarios.length ? response.scenarios.map((scenario, index) => <article className="ai-draft" key={`${index}-${scenario.name}`}><div className="ai-draft-label">DRAFT {String(index + 1).padStart(2, '0')} · UNAPPROVED</div><h4>{scenario.name}</h4>{scenario.description && <p>{scenario.description}</p>}<div className="expected"><span>PROPOSED OUTCOME</span><p>{scenario.expected_outcome}</p></div><div className="ai-draft-foot"><span>{scenario.steps.length} browser steps</span><button className="link-button" onClick={() => onReview({ ...scenario, approved: false })}>Review in editor <ArrowRight size={15} /></button></div></article>) : <div className="ai-no-drafts"><CircleHelp size={20} /><strong>No scenarios proposed</strong><p>The supplied context was not enough to form a check. Answer the questions, add more facts, and generate again.</p></div>}</div><aside className="ai-notes"><div className="ai-note-section"><h4><CircleHelp size={17} />Questions <span>{response.questions.length}</span></h4>{response.questions.length ? <ol>{response.questions.map((item, index) => <li key={index}>{item}</li>)}</ol> : <p>No open questions returned.</p>}</div><div className="ai-note-section"><h4><Info size={17} />Assumptions <span>{response.assumptions.length}</span></h4>{response.assumptions.length ? <ol>{response.assumptions.map((item, index) => <li key={index}>{item}</li>)}</ol> : <p>No assumptions returned.</p>}</div></aside></div><div className="ai-results-foot"><Check size={15} />Suggestions enter coverage only when you save them in the editor.<button type="button" onClick={() => { setResponse(null); setRequestError('') }}><RefreshCw size={14} />Start over</button></div></div>}
  </section>
}
