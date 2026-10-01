import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  AlertCircle, ArrowRight, Check, CircleHelp, ExternalLink, Info, KeyRound, LoaderCircle,
  LockKeyhole, RefreshCw, ShieldCheck, Sparkles, Square, X,
} from 'lucide-react'
import { api, providerName, type AiProvider, type AiConfig, type AiProposalResponse, type ChatGptLogin, type ChatGptModel, type ChatGptProfile, type ChatGptStatus, type OpenCodeStatus, type ScenarioInput } from './api'
import { OpenCodeConnection } from './OpenCodeConnection'
import { SnapshotBrowser } from './RepositorySnapshots'
import { DiscoveryBrowser } from './Discoveries'

type CredentialMode = 'managed' | 'byok' | 'chatgpt' | 'opencode'

function accountName(profile: ChatGptProfile): string {
  const name = profile.label || profile.email || 'ChatGPT account'
  return `${name}${profile.email && profile.email !== name ? ` · ${profile.email}` : ''} · ${profile.id.slice(0, 8)}`
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback
}

export default function AiProposals({ token, projectId, onClose, onReview }: {
  token: string
  projectId: string
  onClose: () => void
  onReview: (scenario: ScenarioInput) => void
}) {
  const [config, setConfig] = useState<AiConfig | null>(null)
  const [configLoading, setConfigLoading] = useState(true)
  const [configError, setConfigError] = useState('')
  const [provider, setProvider] = useState<AiProvider>('openai')
  const [workspaceId, setWorkspaceId] = useState('')
  const [model, setModel] = useState('')
  const [credentialMode, setCredentialMode] = useState<CredentialMode>('managed')
  const [chatgpt, setChatgpt] = useState<ChatGptStatus | null>(null)
  const [selectedProfileId, setSelectedProfileId] = useState('')
  const [chatgptModels, setChatgptModels] = useState<ChatGptModel[]>([])
  const [chatgptBusy, setChatgptBusy] = useState(false)
  const [chatgptModelsLoading, setChatgptModelsLoading] = useState(false)
  const [chatgptError, setChatgptError] = useState('')
  const [chatgptNotice, setChatgptNotice] = useState('')
  const [openCode, setOpenCode] = useState<OpenCodeStatus | null>(null)
  const [openCodeBusy, setOpenCodeBusy] = useState(false)
  const [providerKey, setProviderKey] = useState('')
  const [prompt, setPrompt] = useState('')
  const [context, setContext] = useState('')
  const [snapshotIds, setSnapshotIds] = useState<string[]>([])
  const [discoveryId, setDiscoveryId] = useState('')
  const [consent, setConsent] = useState(false)
  const [response, setResponse] = useState<AiProposalResponse | null>(null)
  const [requestError, setRequestError] = useState('')
  const [generating, setGenerating] = useState(false)
  const controller = useRef<AbortController | null>(null)
  const requestNumber = useRef(0)
  const loginId = useRef('')
  const authWindowRef = useRef<Window | null>(null)
  const identity = useRef(0)
  const chatgptRef = useRef<ChatGptStatus | null>(null)
  const selectedProfileIdRef = useRef('')
  const apiProviders = config?.providers ?? (config ? [{ provider: config.provider, models: config.models, managed_available: config.managed_available, byok_available: config.byok_available }] : [])
  const providers = [...apiProviders, ...(config?.opencode_enabled ? [{ provider: 'opencode-go' as const, models: [], managed_available: false, byok_available: false }] : [])]
  const selectedProvider = providers.find(item => item.provider === provider)
  const recipient = providerName(provider)
  const selectedProfile = chatgpt?.profiles.find(profile => profile.id === selectedProfileId)
  const loginPending = chatgpt?.login?.status === 'pending'

  useEffect(() => { selectedProfileIdRef.current = selectedProfileId }, [selectedProfileId])

  function acceptStatus(value: ChatGptStatus) {
    const previousStatus = chatgptRef.current
    const previousId = selectedProfileIdRef.current
    const nextId = value.active_profile_id || (value.profiles.some(profile => profile.id === previousId) ? previousId : value.profiles.find(profile => profile.connected)?.id || '')
    const previousProfile = previousStatus?.profiles.find(profile => profile.id === previousId)
    const nextProfile = value.profiles.find(profile => profile.id === nextId)
    if (previousStatus && (previousId !== nextId || previousProfile?.connected !== nextProfile?.connected || previousProfile?.sharing !== nextProfile?.sharing)) invalidateContext()
    chatgptRef.current = value
    selectedProfileIdRef.current = nextId
    setChatgpt(value)
    setSelectedProfileId(nextId)
    if (value.login?.id && value.login.status === 'pending') loginId.current = value.login.id
    else if (loginId.current && value.login?.id === loginId.current && value.login.status !== 'pending') loginId.current = ''
  }

  useEffect(() => {
    const abort = new AbortController()
    identity.current += 1
    loginId.current = ''
    chatgptRef.current = null
    selectedProfileIdRef.current = ''
    setChatgpt(null)
    setChatgptModels([])
    setSelectedProfileId('')
    setChatgptBusy(false)
    setConfigLoading(true)
    setConfigError('')
    void api<AiConfig>(token, '/api/ai/config', { signal: abort.signal }).then(value => {
      setConfig(value)
      setOpenCode(null)
      chatgptRef.current = value.chatgpt ?? null
      setChatgpt(value.chatgpt ?? null)
      const initialProfileId = value.chatgpt?.active_profile_id || value.chatgpt?.profiles.find(profile => profile.connected)?.id || ''
      selectedProfileIdRef.current = initialProfileId
      setSelectedProfileId(initialProfileId)
      const choices = value.providers ?? [value]
      const first = choices.find(item => item.models.length && (item.managed_available || item.byok_available))
      const initial = first ?? choices.find(item => item.provider === 'openai')
      const useOpenCode = !first && !value.chatgpt?.enabled && value.opencode_enabled
      setProvider(useOpenCode ? 'opencode-go' : initial?.provider ?? 'openai')
      const mode = useOpenCode ? 'opencode' : initial?.managed_available ? 'managed' : initial?.byok_available ? 'byok' : value.chatgpt?.enabled ? 'chatgpt' : 'managed'
      setCredentialMode(mode)
      setProviderKey('')
      setWorkspaceId('')
      setModel(mode === 'chatgpt' ? '' : initial?.models[0] ?? '')
    }).catch(error => {
      if (!abort.signal.aborted) setConfigError(error instanceof Error ? error.message : 'Could not load AI configuration.')
    }).finally(() => { if (!abort.signal.aborted) setConfigLoading(false) })
    return () => {
      abort.abort()
      identity.current += 1
      controller.current?.abort()
      requestNumber.current += 1
      const pendingLoginId = loginId.current
      loginId.current = ''
      authWindowRef.current?.close()
      authWindowRef.current = null
      if (pendingLoginId) void api(token, '/api/ai/chatgpt/login/cancel', { method: 'POST', body: JSON.stringify({ login_id: pendingLoginId }) }).catch(() => {})
    }
  }, [token, projectId])

  useEffect(() => {
    if (credentialMode !== 'chatgpt' || !chatgpt?.enabled || !selectedProfile?.connected || !selectedProfile.sharing || chatgpt.active_profile_id !== selectedProfile.id) {
      setChatgptModels([])
      setChatgptModelsLoading(false)
      if (credentialMode === 'chatgpt') setModel('')
      return
    }
    const abort = new AbortController()
    setChatgptModelsLoading(true)
    setChatgptModels([])
    setModel('')
    void api<ChatGptModel[]>(token, `/api/ai/chatgpt/models?profile_id=${encodeURIComponent(selectedProfile.id)}`, { signal: abort.signal }).then(models => {
      setChatgptModels(models)
      setModel(models[0]?.slug ?? '')
    }).catch(error => { if (!abort.signal.aborted) setChatgptError(errorMessage(error, 'Could not load ChatGPT models.')) })
      .finally(() => { if (!abort.signal.aborted) setChatgptModelsLoading(false) })
    return () => abort.abort()
  }, [token, projectId, credentialMode, chatgpt?.active_profile_id, selectedProfile?.id, selectedProfile?.connected, selectedProfile?.sharing])

  useEffect(() => {
    if (!chatgpt?.login?.id || chatgpt.login.status !== 'pending') return
    const abort = new AbortController()
    const currentIdentity = identity.current
    let polling = false
    const timer = window.setInterval(() => {
      if (polling) return
      polling = true
      void api<ChatGptStatus>(token, '/api/ai/chatgpt', { signal: abort.signal }).then(value => {
        if (currentIdentity !== identity.current) return
        acceptStatus(value)
        if (value.login?.status && value.login.status !== 'pending' && value.login.message) setChatgptNotice(value.login.message)
      }).catch(error => { if (!abort.signal.aborted) setChatgptError(errorMessage(error, 'Could not check ChatGPT connection.')) })
        .finally(() => { polling = false })
    }, 2000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [token, projectId, chatgpt?.login?.id, chatgpt?.login?.status])

  const configured = Boolean(config && ((providers.some(item => item.models.length > 0 && (item.managed_available || item.byok_available))) || config.chatgpt?.enabled || config.opencode_enabled))
  const chosenModelAvailable = credentialMode === 'opencode' ? Boolean(openCode?.models.some(item => item.id === model)) : credentialMode === 'chatgpt' ? chatgptModels.some(item => item.slug === model) : Boolean(selectedProvider?.models.includes(model))
  const credentialReady = credentialMode === 'opencode' ? Boolean(openCode?.connected && !openCodeBusy) : credentialMode === 'chatgpt' ? Boolean(selectedProfile?.connected && selectedProfile.sharing && selectedProfileId && chatgpt?.active_profile_id === selectedProfileId && !chatgptModelsLoading) : credentialMode === 'managed' ? Boolean(selectedProvider?.managed_available) : Boolean(selectedProvider?.byok_available && providerKey.trim())
  const canGenerate = configured && !generating && !chatgptBusy && !openCodeBusy && (credentialMode !== 'chatgpt' || !loginPending) && Boolean(prompt.trim() && (context.trim() || snapshotIds.length || discoveryId) && chosenModelAvailable && consent && credentialReady)
  const selectedSources = [snapshotIds.length ? `${snapshotIds.length} selected repository ${snapshotIds.length === 1 ? 'snapshot' : 'snapshots'}` : '', discoveryId ? 'one selected discovery' : ''].filter(Boolean).join(' and ')

  function invalidateContext() {
    requestNumber.current += 1
    controller.current?.abort()
    controller.current = null
    setGenerating(false)
    setResponse(null)
    setConsent(false)
  }

  function changeMode(mode: CredentialMode) {
    if (mode === credentialMode) return
    invalidateContext()
    setRequestError('')
    setCredentialMode(mode)
    setProviderKey('')
    setWorkspaceId('')
    setModel(mode === 'chatgpt' ? '' : selectedProvider?.models[0] ?? '')
  }

  function changeProvider(next: AiProvider) {
    if (next === provider) return
    const chosen = providers.find(item => item.provider === next)
    invalidateContext()
    setRequestError('')
    setProvider(next)
    setProviderKey('')
    setWorkspaceId('')
    setOpenCode(null)
    const mode = next === 'opencode-go' ? 'opencode' : chosen?.managed_available ? 'managed' : chosen?.byok_available ? 'byok' : next === 'openai' && chatgpt?.enabled ? 'chatgpt' : 'byok'
    setCredentialMode(mode)
    setModel(mode === 'chatgpt' ? '' : chosen?.models[0] ?? '')
  }

  async function connectChatgpt(profileId?: string) {
    // Open from the click itself so browsers do not block the authorization tab.
    const authWindow = window.open('', '_blank')
    if (!authWindow) { setChatgptError('Allow pop-ups for this site, then try connecting again.'); return }
    authWindow.opener = null
    authWindowRef.current = authWindow
    invalidateContext()
    setChatgptBusy(true)
    setChatgptError('')
    setChatgptNotice('')
    const currentIdentity = identity.current
    let newLoginId = ''
    try {
      const result = await api<ChatGptLogin>(token, '/api/ai/chatgpt/login', {
        method: 'POST', body: JSON.stringify(profileId ? { profile_id: profileId } : {}),
      })
      newLoginId = result.id
      if (currentIdentity !== identity.current) {
        authWindow.close()
        void api(token, '/api/ai/chatgpt/login/cancel', { method: 'POST', body: JSON.stringify({ login_id: result.id }) }).catch(() => {})
        return
      }
      const authUrl = new URL(result.auth_url)
      if (authUrl.origin !== 'https://auth.openai.com' || authUrl.pathname !== '/api/accounts/authorize') throw new Error('The authorization link was invalid. Try connecting again.')
      loginId.current = result.id
      authWindow.location.href = authUrl.href
      setChatgpt(value => value ? { ...value, login: { id: result.id, status: 'pending', message: '' } } : value)
    } catch (error) {
      authWindow.close()
      if (authWindowRef.current === authWindow) authWindowRef.current = null
      if (newLoginId) {
        loginId.current = ''
        void api(token, '/api/ai/chatgpt/login/cancel', { method: 'POST', body: JSON.stringify({ login_id: newLoginId }) }).catch(() => {})
      }
      if (currentIdentity === identity.current) setChatgptError(errorMessage(error, 'Could not start ChatGPT connection.'))
    } finally {
      if (currentIdentity === identity.current) setChatgptBusy(false)
    }
  }

  async function cancelChatgptLogin() {
    const pendingId = loginId.current || chatgpt?.login?.id
    if (!pendingId) return
    loginId.current = ''
    authWindowRef.current?.close()
    authWindowRef.current = null
    setChatgpt(value => value ? { ...value, login: undefined } : value)
    setChatgptBusy(true)
    const currentIdentity = identity.current
    try {
      await api(token, '/api/ai/chatgpt/login/cancel', { method: 'POST', body: JSON.stringify({ login_id: pendingId }) })
      if (currentIdentity === identity.current) setChatgptNotice('ChatGPT connection cancelled.')
    } catch (error) { if (currentIdentity === identity.current) setChatgptError(errorMessage(error, 'Could not cancel ChatGPT connection.')) }
    finally { if (currentIdentity === identity.current) setChatgptBusy(false) }
  }

  async function refreshChatgpt() {
    setChatgptBusy(true)
    setChatgptError('')
    const currentIdentity = identity.current
    try {
      const status = await api<ChatGptStatus>(token, '/api/ai/chatgpt')
      if (currentIdentity === identity.current) acceptStatus(status)
    } catch (error) { if (currentIdentity === identity.current) setChatgptError(errorMessage(error, 'Could not refresh ChatGPT status.')) }
    finally { if (currentIdentity === identity.current) setChatgptBusy(false) }
  }

  async function updateChatgpt(path: 'select' | 'disconnect', profileId: string) {
    invalidateContext()
    setChatgptBusy(true)
    setChatgptError('')
    setChatgptNotice('')
    const currentIdentity = identity.current
    try {
      const status = await api<ChatGptStatus>(token, `/api/ai/chatgpt/${path}`, {
        method: 'POST', body: JSON.stringify({ profile_id: profileId }),
      })
      if (currentIdentity === identity.current) {
        acceptStatus(status)
        if (status.message) setChatgptNotice(status.message)
        else if (path === 'disconnect') setChatgptNotice('ChatGPT account disconnected.')
      }
    } catch (error) { if (currentIdentity === identity.current) setChatgptError(errorMessage(error, `Could not ${path} this ChatGPT account.`)) }
    finally { if (currentIdentity === identity.current) setChatgptBusy(false) }
  }

  async function generate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setRequestError('')
    if (generating) return
    if (!config || !configured) { setRequestError('AI proposal generation is not configured on this server.'); return }
    if (credentialMode === 'chatgpt' && loginPending) { setRequestError('Finish or cancel ChatGPT connection before generating.'); return }
    if (!chosenModelAvailable) { setRequestError('Choose an available model.'); return }
    if (!prompt.trim() || prompt.length > 4000) { setRequestError('Testing request must be 1–4000 characters.'); return }
    if ((!context.trim() && snapshotIds.length === 0 && !discoveryId) || context.length > 60000) { setRequestError('Add application context, a reviewed repository snapshot, or reviewed discovery observations.'); return }
    if (!consent) { setRequestError(`Confirm consent to send these fields to ${recipient}.`); return }
    if (credentialMode === 'managed' && !selectedProvider?.managed_available) { setRequestError('Managed access is unavailable on this server.'); return }
    if (credentialMode === 'byok' && (!selectedProvider?.byok_available || !providerKey.trim())) { setRequestError('Enter a provider key to use BYOK.'); return }
    if (credentialMode === 'chatgpt' && (!chatgpt?.enabled || !selectedProfile?.connected || !selectedProfile.sharing || chatgpt.active_profile_id !== selectedProfileId)) { setRequestError('Connect and select a ChatGPT account that can share requests.'); return }
    if (credentialMode === 'opencode' && (!openCode?.connected || openCodeBusy)) { setRequestError('Connect your OpenCode Go subscription and choose a model.'); return }

    const abort = new AbortController()
    controller.current = abort
    const currentRequest = ++requestNumber.current
    const transientKey = providerKey.trim()
    const transientWorkspace = workspaceId.trim()
    setProviderKey('')
    setWorkspaceId('')
    setGenerating(true)
    setResponse(null)
    try {
      const result = await api<AiProposalResponse>(token, `/api/projects/${encodeURIComponent(projectId)}/proposals`, {
        method: 'POST',
        signal: abort.signal,
        headers: credentialMode === 'byok' ? { 'X-QA-Provider-Key': transientKey, ...(provider === 'anthropic' && transientWorkspace ? { 'X-QA-Anthropic-Workspace': transientWorkspace } : {}) } : undefined,
        body: JSON.stringify({ provider, prompt: prompt.trim(), context: context.trim(), model, credential_mode: credentialMode, ...(credentialMode === 'chatgpt' ? { chatgpt_profile_id: selectedProfileId } : {}), consent: true, repository_snapshot_ids: snapshotIds, ...(discoveryId ? { discovery_id: discoveryId } : {}) }),
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
    <div className="ai-boundary"><Info size={16} /><span>Proposals use your request, application context, and any reviewed repository snapshots or browser observations you select. Discovery observations describe what the app showed; they do not establish the expected outcome.</span></div>
    <div className="ai-repository-section"><div className="ai-repository-heading"><div><div className="eyebrow">OPTIONAL SOURCE CONTEXT</div><h3>Review repository snapshots</h3><p>Select up to one frontend and one backend snapshot. The complete file contents will be sent with your request after consent.</p></div></div><SnapshotBrowser token={token} projectId={projectId} selected={snapshotIds} onSelectionChange={ids => { setSnapshotIds(ids); invalidateContext() }} /></div>
    <div className="ai-repository-section"><div className="ai-repository-heading"><div><div className="eyebrow">OPTIONAL BROWSER CONTEXT</div><h3>Review discovery observations</h3><p>Select one completed discovery. Observed text must be checked against your requirements before it becomes a test expectation.</p></div></div><DiscoveryBrowser token={token} projectId={projectId} selectedId={discoveryId} onSelectionChange={id => { setDiscoveryId(id); invalidateContext() }} /></div>
    {configLoading ? <div className="ai-config-state"><LoaderCircle size={18} className="spin" />Checking model availability…</div>
      : configError ? <div className="error-banner"><AlertCircle size={17} /><span>Could not load AI configuration: {configError}</span></div>
        : !config || !configured ? <div className="ai-unavailable"><LockKeyhole size={22} /><div><strong>Proposal generation is unavailable</strong><p>No model access is configured on this server. Ask the operator to configure access, then reopen this panel.</p></div></div>
          : <form onSubmit={event => void generate(event)}>
            <div className="ai-form-grid"><div className="ai-main-fields"><label className="field-label" htmlFor="ai-request">Testing request <span>*</span></label><textarea id="ai-request" maxLength={4000} rows={4} placeholder="What business behaviour should be checked? What could go wrong?" value={prompt} onChange={event => { setPrompt(event.target.value); invalidateContext() }} /><div className="input-count">{prompt.length} / 4,000</div><label className="field-label" htmlFor="ai-context">Application context <span className={selectedSources ? 'optional' : undefined}>{selectedSources ? 'optional with reviewed context' : '*'}</span></label><textarea id="ai-context" maxLength={60000} rows={9} placeholder={'Paste requirements, known paths, fixture rules, and independent expected values. Observed app text and repository code alone may not reveal the intended business outcome.'} value={context} onChange={event => { setContext(event.target.value); invalidateContext() }} /><div className="input-count">{context.length.toLocaleString()} / 60,000</div></div>
              <div className="ai-settings">
                <div className="ai-settings-heading"><KeyRound size={17} /><strong>Generation settings</strong></div>
                <label className="field-label" htmlFor="ai-provider">Provider</label>
                <select id="ai-provider" value={provider} onChange={event => changeProvider(event.target.value as AiProvider)}>{providers.map(item => <option key={item.provider} value={item.provider} disabled={!item.models.length && item.provider !== 'opencode-go' && !(item.provider === 'openai' && chatgpt?.enabled)}>{providerName(item.provider)}{!item.models.length && item.provider !== 'opencode-go' && !(item.provider === 'openai' && chatgpt?.enabled) ? ' (unavailable)' : ''}</option>)}</select>
                <div className="field-label ai-access-label">Access mode</div>
                <div className="ai-mode-options">
                  {provider === 'opencode-go' && <label className="chosen"><input type="radio" name="credential-mode" value="opencode" checked readOnly /><span><strong>Go / Go Plus subscription</strong><small>Use the local OpenCode connection</small></span></label>}
                  {selectedProvider?.managed_available && selectedProvider.models.length > 0 && <label className={credentialMode === 'managed' ? 'chosen' : ''}><input type="radio" name="credential-mode" value="managed" checked={credentialMode === 'managed'} onChange={() => changeMode('managed')} /><span><strong>Managed</strong><small>Server configured key</small></span></label>}
                  {selectedProvider?.byok_available && selectedProvider.models.length > 0 && <label className={credentialMode === 'byok' ? 'chosen' : ''}><input type="radio" name="credential-mode" value="byok" checked={credentialMode === 'byok'} onChange={() => changeMode('byok')} /><span><strong>Bring your own key</strong><small>For this request only</small></span></label>}
                  {provider === 'openai' && chatgpt?.enabled && <label className={credentialMode === 'chatgpt' ? 'chosen' : ''}><input type="radio" name="credential-mode" value="chatgpt" checked={credentialMode === 'chatgpt'} onChange={() => changeMode('chatgpt')} /><span><strong>ChatGPT subscription</strong><small>Use a connected account</small></span></label>}
                </div>
                {credentialMode === 'opencode' && <OpenCodeConnection key={token} token={token} disabled={generating} onBusy={setOpenCodeBusy} onStatus={status => { invalidateContext(); setOpenCode(status); setModel('') }} />}
                {credentialMode === 'chatgpt' && chatgpt?.enabled && <div className="ai-chatgpt">
                  <p>Use your ChatGPT plan for proposals. Connect in your browser to authorize requests from this local app; no API key is needed.</p>
                  {chatgpt.profiles.length > 0 && <><label className="field-label" htmlFor="ai-chatgpt-account">Account</label><select id="ai-chatgpt-account" value={selectedProfileId} disabled={chatgptBusy || loginPending} onChange={event => { invalidateContext(); selectedProfileIdRef.current = event.target.value; setSelectedProfileId(event.target.value); setChatgptError(''); setChatgptNotice('') }}><option value="">Choose an account</option>{chatgpt.profiles.map(profile => <option key={profile.id} value={profile.id}>{accountName(profile)}{profile.connected ? '' : ' (disconnected)'}</option>)}</select></>}
                  <div className="ai-chatgpt-actions">
                    {selectedProfile?.connected ? <><span className="ai-connected"><Check size={14} />Connected{selectedProfile.sharing ? '' : ' · sharing unavailable'}</span>{chatgpt.active_profile_id !== selectedProfile.id && selectedProfile.sharing && <button type="button" className="button" disabled={chatgptBusy || loginPending} onClick={() => void updateChatgpt('select', selectedProfile.id)}>Use this account</button>}<button type="button" className="button" disabled={chatgptBusy || loginPending} onClick={() => void connectChatgpt(selectedProfile.id)}>Reconnect account</button><button type="button" className="link-button" disabled={chatgptBusy || loginPending} onClick={() => void updateChatgpt('disconnect', selectedProfile.id)}>Disconnect</button></> : <button type="button" className="button" disabled={chatgptBusy || loginPending} onClick={() => void connectChatgpt(selectedProfile?.id)}>{selectedProfile ? 'Reconnect account' : 'Continue with ChatGPT'}</button>}
                    {chatgpt.profiles.length > 0 && <button type="button" className="link-button" disabled={chatgptBusy || loginPending} onClick={() => void connectChatgpt()}>Add another account</button>}
                    <button type="button" className="link-button" disabled={chatgptBusy} onClick={() => void refreshChatgpt()}><RefreshCw size={13} />Refresh</button>
                  </div>
                  {chatgpt.login?.status === 'pending' && <div className="ai-chatgpt-pending"><LoaderCircle size={14} className="spin" /><span>Finish connecting in the browser tab. Checking for authorization…</span><button type="button" className="link-button" disabled={chatgptBusy} onClick={() => void cancelChatgptLogin()}>Cancel</button></div>}
                  {chatgptError && <div className="error-banner"><AlertCircle size={16} /><span>{chatgptError}</span></div>}
                  {chatgptNotice && <p className="ai-chatgpt-notice">{chatgptNotice}</p>}
                  <a href="https://chatgpt.com/settings/usage" target="_blank" rel="noopener noreferrer">Manage usage <ExternalLink size={12} /></a>
                </div>}
                <label className="field-label" htmlFor="ai-model">Model</label>
                <select id="ai-model" value={model} disabled={credentialMode === 'opencode' ? !openCode?.connected || openCodeBusy : credentialMode === 'chatgpt' && (!selectedProfile?.connected || !selectedProfile.sharing || chatgpt?.active_profile_id !== selectedProfileId || chatgptModelsLoading)} onChange={event => { invalidateContext(); setModel(event.target.value) }}>
                  {credentialMode === 'opencode' ? <><option value="">{openCodeBusy ? 'Loading models…' : 'Choose a Go model'}</option>{openCode?.models.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</> : credentialMode === 'chatgpt' ? <><option value="">{chatgptModelsLoading ? 'Loading models…' : 'Choose a model'}</option>{chatgptModels.map(item => <option key={item.slug} value={item.slug}>{item.display_name || item.slug}</option>)}</> : selectedProvider?.models.map(item => <option key={item} value={item}>{item}</option>)}
                </select>
                {credentialMode === 'chatgpt' && selectedProfile?.connected && !selectedProfile.sharing && <p className="ai-key-note">Reconnect this account to authorize sharing requests.</p>}
                {credentialMode === 'chatgpt' && selectedProfile?.connected && selectedProfile.sharing && chatgpt?.active_profile_id !== selectedProfileId && <p className="ai-key-note">Choose “Use this account” to load its models.</p>}
                {credentialMode === 'chatgpt' && selectedProfile?.connected && selectedProfile.sharing && chatgpt?.active_profile_id === selectedProfileId && !chatgptModelsLoading && chatgptModels.length === 0 && <p className="ai-key-note">No models are available for this account.</p>}
                {credentialMode === 'byok' && <><label className="field-label" htmlFor="ai-provider-key">{recipient} API key</label><input id="ai-provider-key" type="password" autoComplete="off" placeholder="Enter key for this request" value={providerKey} onChange={event => { setProviderKey(event.target.value); invalidateContext() }} />{provider === 'anthropic' && <><label className="field-label" htmlFor="ai-workspace">Anthropic workspace ID <span className="optional">optional</span></label><input id="ai-workspace" type="text" autoComplete="off" placeholder="wrkspc_…" value={workspaceId} onChange={event => { setWorkspaceId(event.target.value); invalidateContext() }} /><p className="ai-workspace-note">Required for a key that is not scoped to one workspace.</p></>}<p className="ai-key-note"><LockKeyhole size={13} />Cleared on submit. Never stored in browser storage.</p></>}
              </div></div>
            <label className="ai-consent"><input type="checkbox" checked={consent} onChange={event => setConsent(event.target.checked)} /><span>{selectedSources ? `I have reviewed the selected context and agree to send my testing request, application context, and ${selectedSources} to ${recipient} to generate proposals.` : `I agree to send my testing request and application context to ${recipient} to generate proposals.`}</span></label>
            {requestError && <div className="error-banner"><AlertCircle size={17} /><span>{requestError}</span></div>}
            <div className="ai-actions"><span><ShieldCheck size={16} />Drafts remain unapproved and unsaved until you review them.</span>{generating ? <button className="button button-danger" type="button" onClick={cancel}><Square size={14} />Cancel request</button> : <button className="button button-primary" type="submit" disabled={!canGenerate}><Sparkles size={16} />Generate proposals</button>}</div>
          </form>}
    {response && <div className="ai-results"><div className="ai-results-head"><div><div className="eyebrow">PROPOSAL REVIEW</div><h3>{response.scenarios.length ? `${response.scenarios.length} draft ${response.scenarios.length === 1 ? 'scenario' : 'scenarios'}` : 'More context needed'}</h3><p>Generated with {response.model}. Review assertions against your own requirements before saving.</p>{response.discovery_id && <p>Discovery source: <code>{response.discovery_id}</code></p>}{response.repository_snapshots && response.repository_snapshots.length > 0 && <p>Repository sources: {response.repository_snapshots.map(item => `${item.repository} (${item.role}, ${item.commit_sha.slice(0, 12)})`).join(" · ")}</p>}</div><span className="ai-provider-badge"><Sparkles size={14} />{(response.credential_mode ?? credentialMode) === 'chatgpt' ? 'Using ChatGPT plan' : providerName(response.provider)}</span></div><div className="ai-results-grid"><div className="ai-drafts">{response.scenarios.length ? response.scenarios.map((scenario, index) => <article className="ai-draft" key={`${index}-${scenario.name}`}><div className="ai-draft-label">DRAFT {String(index + 1).padStart(2, '0')} · UNAPPROVED</div><h4>{scenario.name}</h4>{scenario.description && <p>{scenario.description}</p>}<div className="expected"><span>PROPOSED OUTCOME</span><p>{scenario.expected_outcome}</p></div><div className="ai-draft-foot"><span>{scenario.steps.length} browser steps</span><button className="link-button" onClick={() => onReview({ ...scenario, approved: false })}>Review in editor <ArrowRight size={15} /></button></div></article>) : <div className="ai-no-drafts"><CircleHelp size={20} /><strong>No scenarios proposed</strong><p>The supplied context was not enough to form a check. Answer the questions, add more facts, and generate again.</p></div>}</div><aside className="ai-notes"><div className="ai-note-section"><h4><CircleHelp size={17} />Questions <span>{response.questions.length}</span></h4>{response.questions.length ? <ol>{response.questions.map((item, index) => <li key={index}>{item}</li>)}</ol> : <p>No open questions returned.</p>}</div><div className="ai-note-section"><h4><Info size={17} />Assumptions <span>{response.assumptions.length}</span></h4>{response.assumptions.length ? <ol>{response.assumptions.map((item, index) => <li key={index}>{item}</li>)}</ol> : <p>No assumptions returned.</p>}</div></aside></div><div className="ai-results-foot"><Check size={15} />Suggestions enter coverage only when you save them in the editor.<button type="button" onClick={() => { setResponse(null); setRequestError('') }}><RefreshCw size={14} />Start over</button></div></div>}
  </section>
}
