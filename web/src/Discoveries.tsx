import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertCircle, Check, FileText, Info, LoaderCircle, Play, RefreshCw, ShieldCheck, Square } from 'lucide-react'
import { api, formatDate, type Discovery, type Scenario } from './api'

const active = (item: Discovery) => item.status === 'queued' || item.status === 'running'
const errorText = (error: unknown) => error instanceof Error ? error.message : 'The request failed.'

export function DiscoveryBrowser({ token, projectId, refreshKey = 0, selectedId = '', onSelectionChange }: {
  token: string
  projectId: string
  refreshKey?: number
  selectedId?: string
  onSelectionChange?: (id: string) => void
}) {
  const [discoveries, setDiscoveries] = useState<Discovery[]>([])
  const [details, setDetails] = useState<Record<string, Discovery>>({})
  const [openId, setOpenId] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadingDetail, setLoadingDetail] = useState('')
  const [cancelling, setCancelling] = useState('')
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const listController = useRef<AbortController | null>(null)
  const detailController = useRef<AbortController | null>(null)
  const sequence = useRef(0)

  const load = useCallback(async (showLoading: boolean) => {
    if (!showLoading && listController.current && !listController.current.signal.aborted) return
    listController.current?.abort()
    const abort = new AbortController()
    const current = ++sequence.current
    listController.current = abort
    if (showLoading) setLoading(true)
    try {
      const items = await api<Discovery[]>(token, `/api/projects/${encodeURIComponent(projectId)}/discoveries`, { signal: abort.signal })
      if (current === sequence.current && !abort.signal.aborted) { setDiscoveries(items); setError('') }
    } catch (err) { if (current === sequence.current && !abort.signal.aborted) setError(`Could not load discoveries: ${errorText(err)}`) }
    finally { if (listController.current === abort) listController.current = null; if (current === sequence.current && !abort.signal.aborted) setLoading(false) }
  }, [token, projectId])

  useEffect(() => {
    setDiscoveries([]); setDetails({}); setOpenId(''); setLoadingDetail(''); setError('')
    void load(true)
    return () => { sequence.current += 1; listController.current?.abort(); detailController.current?.abort() }
  }, [load, refreshKey, reload])

  useEffect(() => {
    if (!discoveries.some(active)) return
    const timer = window.setInterval(() => void load(false), 3000)
    return () => window.clearInterval(timer)
  }, [discoveries, load])

  async function review(id: string) {
    if (openId === id && details[id]) { if (selectedId !== id) setOpenId(''); return }
    if (selectedId && selectedId !== id) onSelectionChange?.('')
    detailController.current?.abort()
    const abort = new AbortController()
    detailController.current = abort
    setOpenId(id); setLoadingDetail(id); setError('')
    try {
      const item = await api<Discovery>(token, `/api/discoveries/${encodeURIComponent(id)}`, { signal: abort.signal })
      if (!abort.signal.aborted) setDetails(previous => ({ ...previous, [id]: item }))
    } catch (err) { if (!abort.signal.aborted) setError(`Could not load discovery observations: ${errorText(err)}`) }
    finally { if (!abort.signal.aborted) setLoadingDetail('') }
  }

  async function cancel(id: string) {
    setCancelling(id); setError('')
    try {
      await api<Discovery>(token, `/api/discoveries/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
      await load(false)
    } catch (err) { setError(`Could not cancel discovery: ${errorText(err)}`) }
    finally { setCancelling('') }
  }

  return <section className="discovery-browser" aria-label="Discovery history">
    <div className="repository-browser-head"><div><h3>Discovery history</h3><p>Completed means pages were observed; it does not mean any check passed.</p></div><button type="button" className="button button-outline" onClick={() => { if (selectedId) onSelectionChange?.(''); setReload(value => value + 1) }} disabled={loading}><RefreshCw size={15} />Refresh</button></div>
    {error && <div className="error-banner" role="alert"><AlertCircle size={16} /><span>{error}</span></div>}
    {loading ? <div className="repository-loading"><LoaderCircle className="spin" size={17} />Loading discoveries…</div> : discoveries.length === 0 ? <div className="repository-empty">No browser discovery yet. Start a bounded observation to see paths, controls, and links.</div> : <div className="discovery-list">{discoveries.map(item => {
      const detail = details[item.id]
      const ready = item.status === 'completed'
      return <article className={`discovery-item ${selectedId === item.id ? 'selected' : ''}`} key={item.id}>
        <div className="discovery-item-head"><div><span className={`discovery-status discovery-${item.status}`}>{item.status}</span><h4>{item.start_path}</h4><p>{formatDate(item.created_at)} · up to {item.max_pages} {item.max_pages === 1 ? 'page' : 'pages'}{item.setup_scenario ? ` · setup: ${item.setup_scenario.name}` : ''}</p></div><div className="discovery-item-actions">{active(item) && <button type="button" className="button button-danger" onClick={() => void cancel(item.id)} disabled={cancelling === item.id}>{cancelling === item.id ? <LoaderCircle size={14} className="spin" /> : <Square size={13} />}Cancel</button>}{ready && <button type="button" className="link-button" onClick={() => void review(item.id)} aria-expanded={openId === item.id}>{openId === item.id ? detail ? selectedId === item.id ? 'Observations open' : 'Hide observations' : loadingDetail === item.id ? 'Loading…' : 'Retry review' : 'Review observations'}</button>}</div></div>
        {active(item) && <p className="discovery-status-note"><LoaderCircle size={14} className="spin" />The browser is observing this project. Results will appear here automatically.</p>}
        {item.status === 'error' && <p className="discovery-error"><AlertCircle size={14} />{item.error || 'Discovery failed.'}</p>}
        {item.status === 'cancelled' && <p className="discovery-status-note">Discovery was cancelled. No observations are available for proposals.</p>}
        {openId === item.id && (loadingDetail === item.id ? <div className="repository-loading"><LoaderCircle size={16} className="spin" />Loading complete observations…</div> : detail?.result && <div className="discovery-observations">
          <p className="discovery-observation-note"><Info size={16} />Observed text and values describe what the browser saw. Verify intended outcomes from your requirements before approving a scenario.</p>
          {detail.result.limited && <p className="discovery-limit"><AlertCircle size={15} />This observation reached a page or content limit. Review the coverage notes below.</p>}
          {detail.result.warnings.length > 0 && <div className="discovery-warnings"><strong>Coverage notes</strong><ul>{detail.result.warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul></div>}
          {detail.result.pages.map((page, index) => <section className="discovery-page" key={`${page.path}-${index}`}><div className="discovery-page-head"><span>PAGE {index + 1}</span><h5>{page.path}</h5>{page.truncated && <span className="discovery-truncated">Content truncated</span>}</div>{page.title && <p><strong>Title</strong> {page.title}</p>}
            <div className="discovery-page-grid"><div><strong>Headings · {page.headings.length}</strong>{page.headings.length ? <ul>{page.headings.map((heading, headingIndex) => <li key={headingIndex}>{heading}</li>)}</ul> : <p>No headings observed.</p>}</div><div><strong>Links · {page.links.length}</strong>{page.links.length ? <ul>{page.links.map((link, linkIndex) => <li key={linkIndex}><code>{link.path}</code>{link.text && ` — ${link.text}`}</li>)}</ul> : <p>No eligible links observed.</p>}</div></div>
            <div className="discovery-elements"><strong>Controls and test IDs · {page.elements.length}</strong>{page.elements.length ? <div className="discovery-element-list">{page.elements.map((element, elementIndex) => <div key={elementIndex}><code>{element.test_id || 'no test ID'}</code><span>{[element.tag, element.role, element.input_type].filter(Boolean).join(' · ')}</span>{element.label && <p>Label: {element.label}</p>}{element.text && <p>Text: {element.text}</p>}</div>)}</div> : <p>No controls or test IDs observed.</p>}</div>
          </section>)}
          {onSelectionChange && <button type="button" className={selectedId === item.id ? 'button button-outline' : 'button button-primary'} onClick={() => onSelectionChange(selectedId === item.id ? '' : item.id)}>{selectedId === item.id ? <Check size={15} /> : <FileText size={15} />}{selectedId === item.id ? 'Selected for proposal' : 'Use these reviewed observations'}</button>}
        </div>)}
      </article>
    })}</div>}
  </section>
}

export default function Discoveries({ token, projectId, approvedScenarios }: { token: string; projectId: string; approvedScenarios: Scenario[] }) {
  const [startPath, setStartPath] = useState('/')
  const [maxPages, setMaxPages] = useState(3)
  const [setupId, setSetupId] = useState('')
  const [starting, setStarting] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [refreshKey, setRefreshKey] = useState(0)
  const controller = useRef<AbortController | null>(null)
  const sequence = useRef(0)

  useEffect(() => () => { sequence.current += 1; controller.current?.abort() }, [token, projectId])

  async function start(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setError(''); setNotice('')
    if (!startPath.startsWith('/') || startPath.startsWith('//') || startPath.includes('?') || startPath.includes('#')) { setError('Enter a same-origin path starting with one / and without query or fragment.'); return }
    const abort = new AbortController(); controller.current = abort
    const current = ++sequence.current
    setStarting(true)
    try {
      const result = await api<Discovery>(token, `/api/projects/${encodeURIComponent(projectId)}/discoveries`, {
        method: 'POST', signal: abort.signal,
        body: JSON.stringify({ start_path: startPath, max_pages: maxPages, ...(setupId ? { setup_scenario_id: setupId } : {}) }),
      })
      if (current === sequence.current && !abort.signal.aborted) { setNotice(`Discovery queued for ${result.start_path}. Watch its status below.`); setRefreshKey(value => value + 1) }
    } catch (err) { if (current === sequence.current && !abort.signal.aborted) setError(errorText(err)) }
    finally { if (current === sequence.current) { setStarting(false); controller.current = null } }
  }

  return <><div className="page-heading"><div><div className="eyebrow">BROWSER OBSERVATION</div><h1>Discover the app</h1><p>Inventory visible paths and controls in a bounded browser visit.</p></div></div>
    <div className="discovery-layout"><form className="panel discovery-start" onSubmit={event => void start(event)}><div className="panel-kicker">NEW DISCOVERY</div><h2>Choose a starting point</h2><p className="field-help">The browser visits up to five same-origin pages in a fresh context. It follows ordinary links without submitting forms or clicking discovered buttons.</p>
      <div className="discovery-form-row"><div><label className="field-label" htmlFor="discovery-path">Start path <span>*</span></label><input id="discovery-path" value={startPath} onChange={event => setStartPath(event.target.value)} placeholder="/" /></div><div><label className="field-label" htmlFor="discovery-limit">Page limit</label><select id="discovery-limit" value={maxPages} onChange={event => setMaxPages(Number(event.target.value))}>{[1, 2, 3, 4, 5].map(value => <option key={value} value={value}>{value}</option>)}</select></div></div>
      <label className="field-label" htmlFor="discovery-setup">Approved setup scenario <span className="optional">optional</span></label><select id="discovery-setup" value={setupId} onChange={event => setSetupId(event.target.value)}><option value="">None — start without setup</option>{approvedScenarios.map(scenario => <option value={scenario.id} key={scenario.id}>{scenario.name}</option>)}</select><p className="field-help">If selected, its approved steps run exactly as saved before observing pages.</p>
      {error && <div className="error-banner" role="alert"><AlertCircle size={16} /><span>{error}</span></div>}{notice && <div className="repository-success" role="status"><Check size={16} />{notice}</div>}
      <button className="button button-primary" type="submit" disabled={starting}>{starting ? <LoaderCircle size={16} className="spin" /> : <Play size={16} />}{starting ? 'Starting…' : 'Start discovery'}</button>
    </form><aside className="repository-explainer"><ShieldCheck size={20} /><div><strong>Observations are not test results</strong><p>Discovery records what the browser saw, including text and controls. It does not decide whether those values are correct. Use a designated test environment because GET routes can still have side effects.</p></div></aside></div>
    <DiscoveryBrowser token={token} projectId={projectId} refreshKey={refreshKey} />
  </>
}
