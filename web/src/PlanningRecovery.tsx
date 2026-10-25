import type { ChatTurn } from './api'

export default function PlanningRecovery({ turn, disabled, onRetry, onSettings }: {
  turn: ChatTurn
  disabled: boolean
  onRetry: (prompt: string) => void
  onSettings?: () => void
}) {
  const needsConnection = turn.failure && !turn.failure.retryable
  return <section className="planning-recovery" aria-label="Planning recovery">
    <div role="alert"><strong>Planning didn’t finish</strong><p>{turn.reply}</p></div>
    {turn.failure?.detail && <details><summary>Planning detail</summary><p>{turn.failure.detail}</p></details>}
    <p>Your request can be restored below. Review the AI connection and available context before sending again. Retrying drafts a plan; tests still require approval.</p>
    <div className="flow-row">
      {needsConnection && onSettings && <button className="button button-outline" disabled={disabled} onClick={onSettings}>Review AI connection</button>}
      <button className="button button-outline" disabled={disabled} onClick={() => onRetry(turn.prompt)}>{needsConnection ? 'Review request' : 'Review and retry'}</button>
    </div>
  </section>
}
