import type { Run } from './api'
import { journeyStages } from './workspace'

export default function JourneyProgress({ run }: { run: Run }) {
  const journeys = run.scenarios.filter((s) => s.steps.some((step) => step.action === 'phase'))
  if (!journeys.length) return null
  return (
    <div className="journey-progress" aria-label="Journey stage results">
      {journeys.map((scenario) => {
        const result = run.results.find((r) => r.scenario_id === scenario.id)
        const stages = journeyStages(
          scenario.steps,
          result,
          run.status === 'queued' || run.status === 'running'
        )
        return (
          <section key={scenario.id}>
            <h3>{scenario.name}</h3>
            <p>
              {stages.filter((s) => s.status === 'passed').length} of {stages.length} stages
              completed
            </p>
            <ol>
              {stages.map((stage, index) => (
                <li key={index}>
                  <div>
                    <strong>{stage.name}</strong>
                    <small>
                      {stage.actors.length
                        ? stage.actors.join(', ').replaceAll('_', ' ') + ' · '
                        : ''}
                      {stage.passed}/{stage.total} steps completed
                    </small>
                  </div>
                  <span
                    className={`pill pill-${stage.status === 'passed' ? 'passed' : stage.status === 'failed' ? 'failed' : stage.status === 'error' ? 'error' : 'neutral'}`}
                  >
                    {stage.status === 'not_run'
                      ? 'Not run — earlier step failed'
                      : stage.status === 'unfinished'
                        ? 'Not completed'
                        : stage.status}
                  </span>
                </li>
              ))}
            </ol>
          </section>
        )
      })}
    </div>
  )
}
