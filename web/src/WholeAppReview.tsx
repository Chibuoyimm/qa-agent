import { Fragment, useState } from 'react'
import type { AiProposalResponse, SourceSurvey } from './api'

const sourceStatus: Record<SourceSurvey['status'], string> = {
  scanning: 'Reading source', live: 'Read live', saved: 'Used saved files', no_files: 'No files selected', unavailable: 'Could not read',
}

function EvidenceFiles({source, paths}: {source: SourceSurvey; paths: string[]}) {
  function fileURL(path: string) {
    if (!/^[0-9a-f]{40}$/i.test(source.commit_sha)) return undefined
    if (source.provider === 'github') {
      const repo = source.repository.split('/').map(encodeURIComponent).join('/')
      return `https://github.com/${repo}/blob/${source.commit_sha}/${path.split('/').map(encodeURIComponent).join('/')}`
    }
    if (source.provider === 'azure' && source.repository.startsWith('https://dev.azure.com/')) {
      const url = new URL(source.repository)
      url.searchParams.set('path', '/' + path)
      url.searchParams.set('version', 'GC' + source.commit_sha)
      return url.href
    }
  }
  return <>{paths.map((path, index) => <Fragment key={path}>{index > 0 && ', '}
    <a href={fileURL(path)} target="_blank" rel="noreferrer">{path}</a></Fragment>)}</>
}

type PlanProps = { proposal: AiProposalResponse; turnId: string }

export function SetupJourneys({ proposal, turnId }: PlanProps) {
  const [filter, setFilter] = useState('')
  const plan = proposal.whole_app
  const setup = plan?.needs_setup
  if (!plan || !setup?.length) return null
  const query = filter.trim().toLowerCase()
  const journeys = setup.filter(journey => !query ||
    [journey.name, journey.expected_outcome, ...journey.missing_setup].some(text => text.toLowerCase().includes(query)))
  return <section className="setup-journeys" aria-labelledby={`setup-${turnId}`}>
    <h3 id={`setup-${turnId}`} tabIndex={-1}>Needs setup ({setup.length})</h3>
    {setup.length > 8 && <div className="setup-filter">
      <label htmlFor={`filter-${turnId}`}>Find a journey or prerequisite</label>
      <input id={`filter-${turnId}`} type="search" value={filter} onChange={event => setFilter(event.target.value)}/>
      {query && <span role="status">{journeys.length} of {setup.length} shown</span>}
    </div>}
    {journeys.map((journey, i) =>
      <details className="setup-journey" key={setup.indexOf(journey)} open={i === 0}>
        <summary>{journey.name}</summary>
        <p>{journey.expected_outcome}</p>
        <strong>What’s missing</strong>
        <ul>{journey.missing_setup.map((item, index) => <li key={index}>{item}</li>)}</ul>
        <details className="setup-sources"><summary>Source evidence</summary>
          {plan.sources.map((source, sourceIndex) => {
            const paths = source.journeys.filter((_, j) => journey.source_refs.includes(`R${sourceIndex + 1}.J${j + 1}`)).flatMap(item => item.paths)
            return paths.length > 0 && <p key={source.source_id}><strong>{source.repository}</strong> · <EvidenceFiles source={source} paths={[...new Set(paths)]}/></p>
          })}
        </details>
      </details>)}
  </section>
}

export default function WholeAppReview({ proposal, turnId }: PlanProps) {
  const plan = proposal.whole_app
  if (!plan) return null
  const candidates = plan.sources.flatMap((source, i) => source.journeys.map((journey, j) => ({
    id: `R${i + 1}.J${j + 1}`, source, journey,
  })))
  const setup = plan.needs_setup ?? []
  const covered = new Set([...plan.coverage.flat(), ...setup.flatMap(journey => journey.source_refs)])
  const outside = candidates.filter(candidate => !covered.has(candidate.id)).map(({source, journey}) => `${journey.name} · ${source.repository}`)
  const gaps = plan.sources.flatMap(source => source.gaps.map(gap => `${source.repository} · ${gap}`))
  return <section className="whole-app-review" aria-label="Whole-app coverage">
    <h3>Whole-app coverage</h3>
    <p>{plan.sources.length} project {plan.sources.length === 1 ? 'repository' : 'repositories'} · {proposal.scenarios.length} ready for review{setup.length > 0 && <> · <button className="link-button" type="button" onClick={() => document.getElementById(`setup-${turnId}`)?.focus()}>{setup.length} {setup.length === 1 ? 'needs' : 'need'} setup</button></>}</p>
    {plan.sources.filter(source => source.note).map(source => <p key={source.source_id}><strong>{source.repository}</strong> · {source.note}</p>)}
    <details><summary>Repository evidence</summary>{plan.sources.map(source => <div key={source.source_id}>
      <strong>{source.repository}</strong> · {sourceStatus[source.status]} · {source.ref} {source.commit_sha && <>· <code>{source.commit_sha.slice(0,12)}</code></>}
      <p>{source.eligible_files ? `Read ${source.paths.length} of ${source.eligible_files} eligible ${source.eligible_files === 1 ? 'file' : 'files'}` : `${source.paths.length} ${source.paths.length === 1 ? 'file' : 'files'} inspected`}{(source.batches_read ?? 0)>1 && <> in {source.batches_read} batches</>}{source.inventory_excluded ? <> · {source.inventory_excluded} inventory entries excluded</> : null}{source.paths.length > 0 && <> · <EvidenceFiles source={source} paths={source.paths} /></>}</p>
    </div>)}
    {proposal.scenarios.map((scenario,i) => <div key={i}><strong>{scenario.name}</strong><ul>
      {candidates.filter(candidate=>plan.coverage[i]?.includes(candidate.id)).map(({id,source,journey})=>
        <li key={id}>{source.repository} · {journey.name} · <EvidenceFiles source={source} paths={journey.paths} /></li>)}
    </ul></div>)}</details>
    {(outside.length>0 || gaps.length>0) && <details><summary>Additional source findings ({outside.length+gaps.length})</summary><ul>{[...outside,...gaps].map((line,i)=><li key={i}>{line}</li>)}</ul></details>}
  </section>
}
