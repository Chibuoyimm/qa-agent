import { useEffect, useRef, useState } from 'react'
import { AlertCircle, ArrowRight, Code2, FileJson2, Layers3, LoaderCircle, Plus, Trash2, X } from 'lucide-react'
import { api, defaultStep, emptyScenario, validateScenario, locatorRoles, type Scenario, type ScenarioInput, type Step, type StepAction } from './api'
import { requiresPlanRevision } from './workspace'
type EditorMode = 'guided' | 'json'
const actionText: Record<StepAction, string> = {
  navigate: 'Navigate', fill: 'Fill field', click: 'Click',
  assert_text: 'Assert exact text', assert_visible: 'Assert visible',
}
const actionHint: Record<StepAction, string> = {
  navigate: 'Open a same-origin path, for example /dashboard.',
  fill: 'Enter literal text or a worker-local QA_TEST_ secret.',
  click: 'Select a uniquely named control.',
  assert_text: 'Compare the element’s full text exactly.',
  assert_visible: 'Require the selected control to be visible.',
}
const sampleScenario: ScenarioInput = {
  name: 'Net revenue excludes refunds and cancelled orders',
  description: 'Sign in to the controlled sample and verify its seeded revenue summary.',
  expected_outcome: 'Net revenue is 140000 from paid 150000 less refund 10000; the cancelled order is excluded.',
  approved: false,
  steps: [
    { action: 'navigate', path: '/login' },
    { action: 'fill', test_id: 'login-email', secret_env: 'QA_TEST_EMAIL' },
    { action: 'fill', test_id: 'login-password', secret_env: 'QA_TEST_PASSWORD' },
    { action: 'click', test_id: 'login-submit' },
    { action: 'assert_text', test_id: 'dashboard-revenue', value: '140000' },
    { action: 'assert_text', test_id: 'dashboard-orders', value: '3 orders' },
  ],
}

function ErrorBanner({message}:{message:string}) { return <div className="error-banner" role="alert"><AlertCircle size={17}/>{message}</div> }
export default function ScenarioEditor({ token, projectId, initial, source, onClose, onSaved, onDraft }: { token: string; projectId: string; initial: ScenarioInput | null; source: 'new' | 'copy' | 'proposal'; onClose: () => void; onSaved: (scenario: Scenario) => void; onDraft?: (scenario: ScenarioInput) => void }) {
  const drawerRef = useRef<HTMLElement>(null)
  const [mode, setMode] = useState<EditorMode>('guided')
  const [draft, setDraft] = useState<ScenarioInput>(() => initial ? { ...initial, approved: false, steps: initial.steps.map(step => ({ ...step })) } : emptyScenario())
  const [json, setJson] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [reviewed, setReviewed] = useState(false)
  useEffect(() => {
    const drawer = drawerRef.current
    drawer?.querySelector<HTMLInputElement>('#scenario-name')?.focus()
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') { event.preventDefault(); onClose() }
      if (event.key !== 'Tab' || !drawer) return
      const controls = Array.from(drawer.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),textarea:not(:disabled),select:not(:disabled)'))
      if (controls.length === 0) return
      const first = controls[0], last = controls[controls.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  function updateDraft(patch: Partial<ScenarioInput>) {
    setDraft(previous => ({ ...previous, ...patch }))
    setError(''); setReviewed(false)
  }

  function updateStep(index: number, patch: Partial<Step>) {
    setDraft(previous => ({ ...previous, steps: previous.steps.map((step, i) => i === index ? { ...step, ...patch } : step) }))
    setError(''); setReviewed(false)
  }

  function changeAction(index: number, action: StepAction) {
    setDraft(previous => ({ ...previous, steps: previous.steps.map((step, i) => i === index ? defaultStep(action) : step) }))
    setReviewed(false)
  }

  function setEditorMode(next: EditorMode) {
    if (next === mode) return
    if (next === 'json') {
      setJson(JSON.stringify(draft, null, 2))
      setMode(next)
      setError('')
      return
    }
    try {
      const parsed = JSON.parse(json) as unknown
      if (Array.isArray(parsed)) throw new Error('Guided mode edits one scenario. Keep JSON mode to import multiple scenarios.')
      setDraft(validateScenario(parsed))
      setMode(next)
      setError(''); setReviewed(false)
    } catch (err) { setError(err instanceof Error ? err.message : 'Invalid JSON') }
  }

  async function save() {
    setError('')
    let saved = 0
    let inputs: ScenarioInput[] = []
    try {
      const raw: unknown = mode === 'json' ? JSON.parse(json) : draft
      inputs = (Array.isArray(raw) ? raw : [raw]).map(validateScenario)
      if (initial && inputs.some(input => requiresPlanRevision(initial, input))) throw new Error('The expected outcome changed but the assertions did not. Update the assertion steps, or revise the plan with the agent, before saving.')
      if (!onDraft && inputs.some(item => item.approved) && !reviewed) throw new Error('Confirm you reviewed the expected outcomes and assertions before approving.')
      if (!token || !projectId) throw new Error('Connection to the project was lost. Close and reopen this editor.')
      if (onDraft) { if (inputs.length !== 1) throw new Error('Edit one check at a time.'); onDraft({ ...inputs[0], approved: false }); onClose(); return }
      setSaving(true)
      for (const input of inputs) {
        const scenario = await api<Scenario>(token, `/api/projects/${encodeURIComponent(projectId)}/scenarios`, {
          method: 'POST', body: JSON.stringify(input),
        })
        saved += 1
        onSaved(scenario)
      }
      if (saved) onClose()
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Could not save scenarios'
      if (saved > 0) {
        const remaining = inputs.slice(saved)
        setJson(JSON.stringify(remaining.length === 1 ? remaining[0] : remaining, null, 2))
        setMode('json')
        setError(`${saved} ${saved === 1 ? 'scenario was' : 'scenarios were'} saved. ${remaining.length} remain in the editor. ${message}`)
      } else setError(message)
    } finally { setSaving(false) }
  }

  return <div className="drawer-backdrop" onMouseDown={event => { if (event.target === event.currentTarget) onClose() }}>
    <section className="drawer" ref={drawerRef} role="dialog" aria-modal="true" aria-labelledby="editor-title">
      <div className="drawer-header"><div><div className="eyebrow">SCENARIO DESIGNER</div><h2 id="editor-title">{onDraft ? 'Edit proposed check' : source === 'copy' ? 'Review a copy' : source === 'proposal' ? 'Review AI proposal' : 'Add a scenario'}</h2><p>{source === 'copy' ? 'This creates a separate scenario. The existing check stays in coverage.' : source === 'proposal' ? 'Verify every proposed expectation and browser step before saving.' : 'Define the outcome, then add browser steps that prove it.'}</p></div><button className="icon-button" onClick={onClose} aria-label="Close editor"><X size={20} /></button></div>
      <div className="editor-tabs"><button className={mode === 'guided' ? 'active' : ''} onClick={() => setEditorMode('guided')}><Layers3 size={16} /> Guided editor</button><button className={mode === 'json' ? 'active' : ''} onClick={() => setEditorMode('json')}><Code2 size={16} /> JSON import</button></div>
      <div className="drawer-body">
        {mode === 'guided' ? <>
          <div className="form-section"><label className="field-label" htmlFor="scenario-name">Scenario name <span>*</span></label><input id="scenario-name" maxLength={200} placeholder="e.g. Net revenue excludes refunds and cancelled orders" value={draft.name} onChange={event => updateDraft({ name: event.target.value })} /></div>
          <div className="form-section"><label className="field-label" htmlFor="scenario-description">Description</label><textarea id="scenario-description" rows={2} maxLength={4000} placeholder="What user journey or business rule does this cover?" value={draft.description} onChange={event => updateDraft({ description: event.target.value })} /></div>
          <div className="form-section"><label className="field-label" htmlFor="scenario-expected">Expected outcome <span>*</span></label><textarea id="scenario-expected" rows={3} maxLength={4000} placeholder="State the business result independently of what the current UI displays." value={draft.expected_outcome} onChange={event => updateDraft({ expected_outcome: event.target.value })} /><p className="field-help">Describe the intended rule. The assertion steps below must prove it; changing this description alone does not change the test.</p></div>
          <div className="steps-heading"><div><div className="field-label">Browser steps <span>*</span></div><p className="field-help">Use paths, exact field labels, role and name, or test IDs. Add at least one assertion for approval.</p></div><span className="count-tag">{draft.steps.length} / 50</span></div>
          <div className="step-list">{draft.steps.map((step, index) => <div className="step-card" key={index}>
            <div className="step-top"><span className="step-number">{String(index + 1).padStart(2, '0')}</span><select aria-label={`Action for step ${index + 1}`} value={step.action} onChange={event => changeAction(index, event.target.value as StepAction)}>{Object.entries(actionText).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select><button className="icon-button small" aria-label={`Remove step ${index + 1}`} disabled={draft.steps.length === 1} onClick={() => updateDraft({ steps: draft.steps.filter((_, i) => i !== index) })}><Trash2 size={16} /></button></div>
            <p className="step-hint">{actionHint[step.action]}</p>
            {step.action === 'navigate' ? <input aria-label={`Path for step ${index + 1}`} placeholder="/dashboard" value={step.path ?? ''} onChange={event => updateStep(index, { path: event.target.value })} /> : <div className="step-fields"><select aria-label={`Find control by for step ${index + 1}`} value={step.locator?.label !== undefined ? 'label' : step.locator ? 'role' : 'test_id'} onChange={event => updateStep(index, event.target.value === 'test_id' ? { test_id: '', locator: undefined } : { test_id: undefined, locator: event.target.value === 'label' ? { label: '' } : { role: 'button', name: '' } })}><option value="label">Field label</option><option value="role">Role and name</option><option value="test_id">Test ID</option></select>
              {step.locator?.label !== undefined ? <input aria-label={`Field label for step ${index + 1}`} placeholder="Exact field label, e.g. Email" value={step.locator.label} onChange={event => updateStep(index, { locator: { label: event.target.value } })} /> : step.locator ? <><select aria-label={`Role for step ${index + 1}`} value={step.locator.role} onChange={event => updateStep(index, { locator: { ...step.locator, role: event.target.value } })}>{locatorRoles.map(role => <option key={role} value={role}>{role}</option>)}</select><input aria-label={`Accessible name for step ${index + 1}`} placeholder="Exact accessible name, e.g. Sign in" value={step.locator.name ?? ''} onChange={event => updateStep(index, { locator: { ...step.locator, name: event.target.value } })} /></> : <input aria-label={`Test ID for step ${index + 1}`} placeholder="data-testid, e.g. dashboard-revenue" value={step.test_id ?? ''} onChange={event => updateStep(index, { test_id: event.target.value })} />}
              {step.action === 'fill' && <><label className="inline-check"><input type="checkbox" checked={step.secret_env !== undefined} onChange={event => { const enabled = event.target.checked; setDraft(previous => ({ ...previous, steps: previous.steps.map((item, i) => i === index ? enabled ? { ...item, value: undefined, secret_env: '' } : { ...item, secret_env: undefined, value: '' } : item) })); setReviewed(false) }} />Use worker-local secret</label><input aria-label={`${step.secret_env !== undefined ? 'Secret environment variable' : 'Value'} for step ${index + 1}`} placeholder={step.secret_env !== undefined ? 'QA_TEST_PASSWORD' : 'Text to enter'} value={step.secret_env !== undefined ? step.secret_env : step.value ?? ''} onChange={event => updateStep(index, step.secret_env !== undefined ? { secret_env: event.target.value } : { value: event.target.value })} /></>}
              {step.action === 'assert_text' && <input aria-label={`Exact text for step ${index + 1}`} placeholder="Exact text expected on screen" value={step.value ?? ''} onChange={event => updateStep(index, { value: event.target.value })} />}
            </div>}</div>)}</div>
          <button className="button button-soft add-step" disabled={draft.steps.length >= 50} onClick={() => updateDraft({ steps: [...draft.steps, defaultStep('assert_visible')] })}><Plus size={16} /> Add step</button>
          {!onDraft && <div className="approval-box"><label className="inline-check strong"><input type="checkbox" checked={draft.approved} onChange={event => updateDraft({ approved: event.target.checked })} />Approve this scenario for runs</label><p>Unapproved scenarios stay in coverage but cannot be selected for a run.</p></div>}
        </> : <>
          <div className="json-intro"><FileJson2 size={20} /><div><strong>Import one scenario or an array</strong><p>Paste JSON using only the fields in the contract. Each valid item is saved as a separate scenario.</p></div></div>
          <button className="sample-template" onClick={() => { setJson(JSON.stringify(sampleScenario, null, 2)); setReviewed(false); setError('') }}>Load controlled sample revenue example <ArrowRight size={14} /></button>
          <textarea className="json-editor" aria-label="Scenario JSON" spellCheck={false} value={json} onChange={event => { setJson(event.target.value); setError(''); setReviewed(false) }} />
          <div className="schema-guide"><strong>Allowed actions</strong><div className="schema-grid"><span><code>navigate</code> → <code>path</code></span><span><code>fill</code> → target + <code>value</code> or <code>secret_env</code></span><span><code>click</code> → target</span><span><code>assert_text</code> → target + exact <code>value</code></span><span><code>assert_visible</code> → target</span></div><p>Targets accept one test ID, <code>locator: {`{label: "Email"}`}</code>, or <code>locator: {`{role: "button", name: "Sign in"}`}</code>. Names must match exactly and identify one control. Secrets use worker-local names beginning with <code>QA_TEST_</code>. JavaScript and arbitrary selectors are not accepted.</p></div>
        </>}
        {!onDraft && <label className="review-check"><input type="checkbox" checked={reviewed} onChange={event => setReviewed(event.target.checked)} /><span>I reviewed the expected outcome and assertion steps for any scenario marked approved.</span></label>}
        {error && <ErrorBanner message={error} />}
      </div>
      <div className="drawer-footer"><button className="button button-ghost" onClick={onClose}>Cancel</button><button className="button button-primary" disabled={saving} onClick={save}>{saving ? <LoaderCircle size={16} className="spin" /> : <Plus size={16} />}{saving ? 'Saving…' : onDraft ? 'Save changes' : source === 'copy' ? 'Save reviewed copy' : 'Create scenario'}</button></div>
    </section>
  </div>
}

