# Testing workspace

The main navigation is **Test, Coverage, Results, Settings**. Conversations, approvals and runs are persisted by the API. A browser refresh clears the operator access token but retains the task URL and server-side history. Draft messages and review edits use tab-local session storage; provider keys and the operator token are never stored there. Model preferences contain only provider, access mode, model and profile identifiers.

## Test a journey

1. In Settings, connect the app, configure an AI connection, and verify browser access under App exploration. For private apps, an engineer can add an approved access check using worker-local `QA_TEST_` secret references.
2. In Test, describe the business behavior and choose or name its journey. Optional bounded exploration finds paths and test IDs; selected repository snapshots provide additional source context. Observations are evidence of current behavior, not the business expectation.
3. Answer any missing business questions. Known access paths, controls and worker-local secret references follow the discovery automatically. Literal access fill values are excluded from model context.
4. Review the proposed outcomes and browser steps. Edit or deselect checks, then approve and run the selected plan. Batch approval is atomic and idempotent, so a network retry reuses the saved checks.
5. Follow the run in the conversation or open its full report. Blocked checks, execution errors and unfinished checks remain separate from assertion failures.

“Run all approved checks” runs existing approved checks. It does not discover or certify every possible feature. Advisory is the default; blocking mode remains available under App access & exploration. Existing project-level conversations and the standalone proposal/editor tools are available in Settings → App & access.

## Evidence, review and recheck

Results display the frozen expected outcome, actual failed assertion text where recorded, ordered executed steps, final screenshots and real browser video. Older runs may lack step records. Missing evidence is reported explicitly. To serve media, the API and worker must share the same `QA_ARTIFACT_DIR`; use an absolute path when their working directories differ. Media requires the operator bearer token, supports range requests, and cannot escape that directory through symlinks. The UI loads authenticated blobs rather than putting tokens into media URLs.

A finding review can record a decision, an optional owner and a note without changing the result or expectation. “Works as intended” requires a reason. Discussing a run in Test shares bounded recorded outcomes and assertion comparisons with the selected provider after consent. It does not apply a code fix.

After deploying a fix, choose **Recheck a deployment** and provide the actual allowed app origin. Rechecks copy the original approved snapshot verbatim, retain a link to the original run, and can use a different deployment target. Both API and worker allowlists still apply. Rechecking does not mutate the app's default target or weaken assertions.

## Coverage

Coverage shows each unique check once using its latest recorded terminal result across the project's history. A later blocked or errored result is unverified; repeated successful runs do not inflate the count. The target and date remain visible.

Journeys are explicit records. Add important journeys before tests exist to keep gaps visible. The headline distinguishes executed checks from journeys; only assertion passes and failures count as checks that ran. Drafts, never-run checks, blocked/error outcomes and untested journeys are identified separately. Older checks without a journey remain available to organize. App discovery alone is not counted as a test run.

## API additions

- Project-scoped `journeys`, `coverage`, and `tasks` endpoints
- Task-scoped `discovery`, `chat`, `approve`, and `runs` endpoints
- Run-scoped `recheck`, `reviews`, and authenticated `artifacts/{scenario_id}/{index}` endpoints

Task and recheck creation use a 32-character hexadecimal `request_id` for retry identity. A changed payload with a reused identity conflicts. Task approval requires a proposal turn from that same task. Legacy API endpoints remain supported. Migration 007 adds the task/journey associations without replacing existing projects, checks, chat, or run snapshots.
