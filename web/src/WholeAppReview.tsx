import { Fragment } from 'react'
import type { AiProposalResponse, SourceSurvey } from './api'

const sourceStatus: Record<SourceSurvey['status'], string> = {
  live: 'Read live', saved: 'Used saved files', no_files: 'No files selected', unavailable: 'Could not read',
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

export default function WholeAppReview({ proposal }: { proposal: AiProposalResponse }) {
  const plan = proposal.whole_app
  if (!plan) return null
  const candidates = plan.sources.flatMap((source, i) => source.journeys.map((journey, j) => ({
    id: `R${i + 1}.J${j + 1}`, source, journey,
  })))
  const covered = new Set(plan.coverage.flat())
  const outside = candidates.filter(candidate => !covered.has(candidate.id)).map(({source, journey}) => `${journey.name} · ${source.repository}`)
  const gaps = plan.sources.flatMap(source => source.gaps.map(gap => `${source.repository} · ${gap}`))
  return <section className="whole-app-review" aria-label="Whole-app coverage">
    <h3>Whole-app coverage</h3>
    <p>{plan.sources.length} project {plan.sources.length === 1 ? 'repository' : 'repositories'} · {proposal.scenarios.length} {proposal.scenarios.length === 1 ? 'journey' : 'journeys'} in this plan</p>
    {plan.sources.filter(source => source.note).map(source => <p key={source.source_id}><strong>{source.repository}</strong> · {source.note}</p>)}
    <details><summary>Repository evidence</summary>{plan.sources.map(source => <div key={source.source_id}>
      <strong>{source.repository}</strong> · {sourceStatus[source.status]} · {source.ref} {source.commit_sha && <>· <code>{source.commit_sha.slice(0,12)}</code></>}
      <p>{source.eligible_files ? `Read ${source.paths.length} of ${source.eligible_files} eligible ${source.eligible_files === 1 ? 'file' : 'files'}` : `${source.paths.length} ${source.paths.length === 1 ? 'file' : 'files'} inspected`}{source.paths.length > 0 && <> · <EvidenceFiles source={source} paths={source.paths} /></>}</p>
    </div>)}
    {proposal.scenarios.map((scenario,i) => <div key={i}><strong>{scenario.name}</strong><ul>
      {candidates.filter(candidate=>plan.coverage[i]?.includes(candidate.id)).map(({id,source,journey})=>
        <li key={id}>{source.repository} · {journey.name} · <EvidenceFiles source={source} paths={journey.paths} /></li>)}
    </ul></div>)}</details>
    {(outside.length>0 || gaps.length>0) && <details><summary>Outside this plan ({outside.length+gaps.length})</summary><ul>{[...outside,...gaps].map((line,i)=><li key={i}>{line}</li>)}</ul></details>}
  </section>
}
