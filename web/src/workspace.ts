import type { CoverageData, Scenario, ScenarioInput, Run, Step, TestingTask, Journey } from './api'

export const errorText = (error: unknown) => error instanceof Error ? error.message : 'Could not complete this request.'
export const scenarioInput = (s: ScenarioInput): ScenarioInput => ({ name: s.name, description: s.description, expected_outcome: s.expected_outcome, approved: s.approved, steps: s.steps.map(step => ({ ...step })) })
export function runCounts(run: Run) {
  const counts = { passed: 0, failed: 0, blocked: 0, error: 0, incomplete: run.scenarios.length - run.results.filter(r=>r.status!=='running').length }
  for (const result of run.results) if(result.status!=='running') counts[result.status]++
  return counts
}
// Each check appears once, using the latest recorded attempt supplied by the API.
export function coverageGroups(data: CoverageData) {
  const latest = new Map(data.checks.map(check => [check.scenario.id, check]))
  const scenarios = new Map(data.scenarios.map(s => [s.id, s]))
  for (const check of data.checks) if (!scenarios.has(check.scenario.id)) scenarios.set(check.scenario.id, check.scenario)
  const groups = data.journeys.map(journey => ({ ...journey, scenarios: [] as Scenario[] }))
  const ungrouped = { id: '', name: 'Checks to organize', project_id: '', created_at: '', scenarios: [] as Scenario[] }
  for (const scenario of scenarios.values()) (groups.find(group => group.id === scenario.journey_id) ?? ungrouped).scenarios.push(scenario)
  if (ungrouped.scenarios.length) groups.push(ungrouped)
  return groups.map(group => {
    const checks = group.scenarios.map(scenario => ({ scenario, latest: latest.get(scenario.id) }))
    return { ...group, checks, executed: checks.filter(check => ['passed', 'failed'].includes(check.latest?.result.status ?? '')).length }
  })
}

export const checkCount = (count: number) => `${count} ${count === 1 ? 'check' : 'checks'}`
export function assertionKey(steps: Step[]) {
 let actor='';const keys:unknown[]=[]
 for(const step of steps){if(step.action==='use_actor')actor=step.value??'';if(step.action.startsWith('assert_'))keys.push([actor,step.action,step.test_id,step.locator?.role,step.locator?.name,step.locator?.label,step.path,step.json_pointer,step.value_ref,step.value??''])}
 return JSON.stringify(keys)
}
export function requiresPlanRevision(original: ScenarioInput, draft: ScenarioInput) {
  return original.expected_outcome !== draft.expected_outcome && assertionKey(original.steps) === assertionKey(draft.steps)
}
export const targetLabel = (step: Step) => step.locator?.label || (step.locator?.role ? `${step.locator.role} “${step.locator.name}”` : (step.test_id ?? 'control').replaceAll('-', ' ').replaceAll('_', ' '))
export function stepLabel(step: Step): string {
  const target = targetLabel(step)
  switch (step.action) {
    case 'phase': return `Stage: ${step.value}`
    case 'use_actor': return `Continue as ${step.value}`
    case 'prepare_fixture': return `Prepare fresh test data with ${step.value}`
    case 'cleanup_fixture': return `Remove this run’s test data with ${step.value}`
    case 'capture_id': return `Save the new record identifier as ${step.save_as}`
    case 'capture_json': return `Save ${step.json_pointer} as ${step.save_as}`
    case 'assert_json': case 'assert_json_number': return `Check saved ${step.json_pointer} equals ${step.value_ref ? `the saved ${step.value_ref}` : step.value}`
    case 'assert_path': return `Check that the page is ${step.path}`
    case 'assert_absent': return `Check that ${target} is absent`
    case 'select_option': return `Choose ${step.value} in ${target}`
    case 'navigate': return `Open ${step.path}`
    case 'fill': return `Enter ${step.secret_env ? 'the saved test credential' : 'the configured value'} in ${target}`
    case 'click': return `Select ${target}`
    case 'assert_visible': return `Check that ${target} is visible`
    case 'assert_text': return `Check that ${target} reads “${step.value}”`
  }
}
export const assertionSummary = (steps: Step[]) => steps.filter(s => s.action.startsWith('assert_')).map(stepLabel).join('. ') + '.'
export function shortTitle(prompt: string) {
  const first = prompt.split(/[.!?\n]/)[0].trim().replace(/^(please\s+)?(test|check|verify)\s+/i, '')
  const words = first.split(/\s+/).slice(0, 7).join(' ')
  const title = (words || 'App checks').slice(0, 60).replace(/[,:;]+$/, '')
  return title.charAt(0).toUpperCase() + title.slice(1)
}
export function runTitle(run: Run, tasks: TestingTask[], journeys: Journey[]) {
  const task = tasks.find(t => t.id === run.task_id)
  return journeys.find(j => j.id === task?.journey_id)?.name || task?.title || run.scenarios[0]?.name || 'App checks'
}
export function compareRuns(original: Run, next: Run) {
  const ids = [...new Set([...original.scenarios, ...next.scenarios].map(s => s.id))]
  return ids.map(id => {
    const before = original.results.find(r => r.scenario_id === id)?.status
    const after = next.results.find(r => r.scenario_id === id)?.status
    const changed = before === 'failed' && after === 'passed' ? 'Fixed' : before === 'failed' && after === 'failed' ? 'Still failing' : before === 'passed' && after === 'failed' ? 'New failure' : after === 'passed' ? 'Passing' : after === 'failed' ? 'Failing' : 'Unverified'
    return { id, name: next.scenarios.find(s => s.id === id)?.name || original.scenarios.find(s => s.id === id)!.name, before: before ?? 'Not completed', after: after ?? 'Not completed', changed }
  })
}
export function groupRuns(runs: Run[]) {
  const byID = new Map(runs.map(run => [run.id, run]))
  const groups = new Map<string, Run[]>()
  for (const run of runs) {
    let root = run
    const seen = new Set([root.id])
    while (root.source_run_id && byID.has(root.source_run_id) && !seen.has(root.source_run_id)) {
      root = byID.get(root.source_run_id)!; seen.add(root.id)
    }
    groups.set(root.id, [...(groups.get(root.id) ?? []), run])
  }
  return [...groups.values()].sort((a,b) => b[0].created_at.localeCompare(a[0].created_at))
}

export function journeySummary(steps: Step[]) {return {actors:[...new Set(steps.filter(s=>s.action==='use_actor').map(s=>s.value!))],phases:steps.filter(s=>s.action==='phase').map(s=>s.value!),fixture:steps.find(s=>s.action==='prepare_fixture')?.value,assertions:steps.filter(s=>s.action.startsWith('assert_')).length}}

export function journeyStages(steps: Step[], result: Run['results'][number] | undefined, running: boolean) {
 const groups: {name:string; indexes:number[]; actors:string[]}[]=[]
 let group:typeof groups[number]|undefined,actor=''
 for(const [index,step] of steps.entries()) {
  if(step.action==='use_actor')actor=step.value??''
  if(step.action==='prepare_fixture'||step.action==='cleanup_fixture'||step.action==='phase') {group={name:step.action==='prepare_fixture'?'Prepare fresh test data':step.action==='cleanup_fixture'?'Clean up test data':step.value??'Journey stage',indexes:[],actors:[]};groups.push(group)}
  if(group){group.indexes.push(index);if(actor&&!['phase','prepare_fixture','cleanup_fixture','use_actor'].includes(step.action)&&!group.actors.includes(actor))group.actors.push(actor)}
 }
 return groups.map(group=>{
  const records=group.indexes.map(index=>result?.step_results?.find(r=>r.index===index))
  const failed=records.find(r=>r&&['failed','error','blocked'].includes(r.status))
  const passed=records.filter(r=>r?.status==='passed').length
  const status=failed?.status??(records.every(r=>r?.status==='not_run')?'not_run':passed===records.length?'passed':running?(passed?'running':'pending'):'unfinished')
  return {...group,status,passed,total:records.length}
 })
}
