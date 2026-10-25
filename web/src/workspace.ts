import type { CoverageData, Scenario, ScenarioInput, Run, Step, TestingTask, Journey } from './api'

export const errorText = (error: unknown) => error instanceof Error ? error.message : 'Could not complete this request.'
export const scenarioInput = (s: ScenarioInput): ScenarioInput => ({ name: s.name, description: s.description, expected_outcome: s.expected_outcome, approved: s.approved, steps: s.steps.map(step => ({ ...step })) })
export function runCounts(run: Run) {
  const counts = { passed: 0, failed: 0, blocked: 0, error: 0, incomplete: run.scenarios.length - run.results.length }
  for (const result of run.results) counts[result.status]++
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
  return JSON.stringify(steps.filter(step => step.action.startsWith('assert_')).map(step => [step.action, step.test_id, step.value ?? '']))
}
export function requiresPlanRevision(original: ScenarioInput, draft: ScenarioInput) {
  return original.expected_outcome !== draft.expected_outcome && assertionKey(original.steps) === assertionKey(draft.steps)
}
export function stepLabel(step: Step): string {
  const target = (step.test_id ?? 'control').replaceAll('-', ' ').replaceAll('_', ' ')
  switch (step.action) {
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
