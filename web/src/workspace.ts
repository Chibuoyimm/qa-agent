import type { CoverageData, Scenario, ScenarioInput, Run } from './api'

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
