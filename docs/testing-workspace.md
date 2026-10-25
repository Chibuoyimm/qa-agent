# Testing workspace

The main navigation is **Test, Coverage, Results, Settings**. Conversations, approvals and runs are persisted by the API. A browser refresh clears the operator access token but retains the task URL and server-side history. Draft messages and review edits use tab-local session storage; provider keys and the operator token are never stored there. Model preferences contain only provider, access mode, model and profile identifiers.

## Test a journey

1. In Settings → App & access, follow the readiness checklist: register the app URL, choose public access or an approved sign-in check, verify browser access, and connect AI. A completed verification can be saved as the app’s default; its date remains visible and new requests reuse its access check. For private apps, an engineer can add an approved access check using worker-local `QA_TEST_` secret references.
2. In Test, describe the business behavior and choose or name its journey. Optional bounded exploration finds paths and named controls; selected repository snapshots provide additional source context. Observations are evidence of current behavior, not the business expectation.
3. Answer any missing business questions. Known access paths, controls and worker-local secret references follow the discovery automatically. Literal access fill values are excluded from model context.
4. Review the proposed outcomes and the exact assertions shown beneath them. Editing prose alone blocks approval until the plan is revised. Use “Revise plan in chat” for a new requirement, or edit an exact checked value directly; this updates its executable assertion and expectation summary. Revised checks require a fresh review before approval, including after reload. Deselect checks as needed, then approve and run the selected plan. Batch approval is atomic and idempotent, so a network retry reuses the saved checks.
5. Follow the run in the conversation or open its full report. Blocked checks, execution errors and unfinished checks remain separate from assertion failures.

“Run all approved checks” runs existing approved checks. It does not discover or certify every possible feature. Advisory is the default; blocking mode remains available under App access & exploration. Existing project-level conversations and the standalone proposal/editor tools are available in Settings → App & access.

## Evidence, review and recheck

Results display the frozen expected outcome, actual failed assertion text where recorded, ordered executed steps, final screenshots and real browser video. Older runs may lack step records. Missing evidence is reported explicitly. To serve media, the API and worker must share the same `QA_ARTIFACT_DIR`; use an absolute path when their working directories differ. Media requires the operator bearer token, supports range requests, and cannot escape that directory through symlinks. The UI loads authenticated blobs rather than putting tokens into media URLs.

A finding review can record a decision, an optional owner and a note without changing the result or expectation. “Works as intended” requires a reason. Discussing a run in Test shares bounded recorded outcomes and assertion comparisons with the selected provider after consent. It does not apply a code fix.

Results use the journey/request name, and history groups rechecks with their source runs. Search names or filter groups by outcome. “What changed?” compares each result with the source: fixed, still failing, new failure, passing, or unverified. The original finding’s review and owner remain visible as context; a recheck can have its own review.

After deploying a fix, choose **Recheck a deployment** and provide the actual allowed app origin. Rechecks copy the original approved snapshot verbatim, retain a link to the original run, and can use a different deployment target. Both API and worker allowlists still apply. Rechecking does not mutate the app's default target or weaken assertions.

## Coverage

Coverage shows each unique check once using its latest recorded terminal result across the project's history. A later blocked or errored result is unverified; repeated successful runs do not inflate the count. The target and date remain visible.

“Test this journey” opens a request with that journey selected and a suggested prompt. New requests can reuse an existing journey or use a short editable name. Coverage badges show outcome counts with neutral styling rather than labeling every observed journey green.

Journeys are explicit records. Add important journeys before tests exist to keep gaps visible. The headline distinguishes executed checks from journeys; only assertion passes and failures count as checks that ran. Drafts, never-run checks, blocked/error outcomes and untested journeys are identified separately. Older checks without a journey remain available to organize. App discovery alone is not counted as a test run.

## API additions

- Project-scoped `journeys`, `coverage`, and `tasks` endpoints
- `GET /api/projects/{id}/access` reads the saved default verification; `POST` with `discovery_id` saves a completed, same-project browser observation. It stores no credentials. Migration 008 adds this metadata.
- Task-scoped `discovery`, `chat`, `approve`, and `runs` endpoints
- Run-scoped `recheck`, `reviews`, and authenticated `artifacts/{scenario_id}/{index}` endpoints

Task and recheck creation use a 32-character hexadecimal `request_id` for retry identity. A changed payload with a reused identity conflicts. Task approval requires a proposal turn from that same task. Legacy API endpoints remain supported. Migration 007 adds the task/journey associations without replacing existing projects, checks, chat, or run snapshots.

The API also rejects a task approval that changes the proposed expected outcome while retaining the same assertions. This catches prose-only edits; it is not a semantic proof that arbitrary prose matches edited code. Review the displayed executable assertions before approval.

## Browser targets

Checks can target an existing `test_id`, an exact field label (`locator: {"label":"Password"}`), or an exact role and accessible name (`locator: {"role":"button","name":"Sign in"}`). Each step accepts one target. Arbitrary CSS, XPath, scripts, and positional fallbacks are unsupported. Existing approved snapshots using test IDs remain valid.

Discovery includes visible headings and publishes an accessible locator only after Playwright confirms that it uniquely resolves to the observed control. Unnamed or duplicate controls may still appear as observations without an executable locator. The planner must ask for missing context rather than invent a target. Execution uses exact matches and rejects ambiguous actions; it never selects the first matching control. Editing a locator changes the assertion definition and remains subject to plan review.

The runner supports navigation, fill, click, native select changes, visible/absent assertions, exact text, and complete journeys described below. Email-inbox actions are not supported. Discovery is bounded to five pages and does not certify an entire app.

## Complete journeys

In Test, enable **Plan a complete journey**. Describe the business outcome for all participants, attach reviewed source context or browser observations, and select a fresh-data profile if an engineer has configured one for this exact target origin. The agent returns one ordered plan with named stages, participant aliases, fixed expectations, and preparation/cleanup steps. Missing routes, controls or expectations become questions before approval. Review the entire plan, then choose **Approve & run journey** once.

The worker keeps a separate browser context for each participant. Switching back preserves that participant’s cookies and session. Created IDs flow forward automatically; amounts, currencies, statuses and other business expectations stay fixed. A failed required step stops later dependent steps, which appear as **Not run**. Results group the recorded steps by stage and retain separate participant recordings. **Run journey again** uses the same approved definition with a new run ID and fresh browser sessions; a selected profile prepares fresh owned data each time.

A complete journey supports up to 200 ordered steps and five named participants; ordinary checks retain their 50-step limit. Planning can take up to six minutes; the worker has a ten-minute run limit. Runs remain leased and durable, and are not automatically replayed after a crash.

### Engineer-installed fresh data

Set `QA_FIXTURE_CONFIG` to the same absolute JSON path for the API and worker, then restart both. [The profile example](../examples/fixtures/profiles.example.json) shows the configuration. Use exact allowed origins. The API only exposes each profile’s ID, name, description, origins and `QA_TEST_` credential reference names. Prepare/cleanup argument arrays and environment values never go to the planner or web client.

The worker runs configured argument arrays directly, without a shell. The model cannot supply a command. Hooks receive `QA_RUN_ID`, `QA_TARGET_ORIGIN`, a private `QA_FIXTURE_STATE_DIR`, minimal process settings, and only the profile’s listed `QA_FIXTURE_` environment references. Preparation emits JSON of the form `{"env":{"QA_TEST_EMAIL":"..."}}` for exactly its registered references. Those values stay in that run’s memory. Do not log credentials or return unregistered fields. Hook output is bounded to 64 KiB, stderr is not recorded, and hook timeouts are at most 60 seconds.

Hooks must derive ownership from the run ID, tolerate partial preparation, and make cleanup idempotent. Cleanup runs after success, required-step failure, graceful shutdown or cancellation. A cleanup failure makes the journey an error and remains visible in Results. Cancellation stays cancelled even when its final cleanup report arrives. An abrupt machine failure or killed worker cannot guarantee cleanup: inspect the saved run, clean records for that run ID using the trusted hook, and then create a new run. Unfinished stages are not evidence that cleanup succeeded.

The included [Escrow FX hook](../examples/fixtures/escrow.py) only permits the disposable local database `escrow_qa_journey` on port 55442. It prepares two synthetic, email-verified customers; the browser must perform KYC and marketplace mutations. Cleanup refuses to remove records involving an unowned participant. It is an example for that app’s schema, not a general database reset routine. The profile requires the escrow repository’s Python environment and migrations. It does not test live email, KYC or payments.

Fresh-data preparation happens only inside an approved run. Automatic pre-plan exploration is skipped when a fresh-data profile is selected; attach source context or previously reviewed observations. Complete journeys cannot serve as discovery sign-in setup, since that would execute their business mutations before journey approval.

### Ordered step contract

- `phase` labels a stage; `use_actor` switches to its `value` alias.
- `prepare_fixture` must be first and `cleanup_fixture` last, with the same selected profile ID in `value`.
- `capture_id` waits for a known URL prefix ending `/` and stores the single identifier segment in `save_as`. It waits for the URL to change from the page before the preceding click/navigation, so a creation route such as `/requests/new` cannot be mistaken for the new record ID.
- `capture_json` reads an identifier JSON pointer (`id` or `*_id`) from a same-origin GET and saves it in `save_as`.
- Later paths and literal fill values may use `{{identifier}}`; `{{run_id}}` is predefined. References must be declared earlier and cannot be overwritten.
- `assert_path` checks an exact same-origin path. `assert_absent` checks a named control is hidden or absent. `select_option` uses the native option value.
- `assert_json` checks a scalar JSON pointer against a fixed `value`, or an earlier identifier via `value_ref`. `assert_json_number` compares a fixed decimal exactly, including API decimal strings with trailing zeros. Observed business values cannot become dynamic expectations.

Read-only JSON checks run through the browser with the current actor’s authenticated cookies, reject redirects, and limit the response to 64 KiB. Navigate the actor to the target app before reading JSON. Paths stay on the approved target origin. They do not persist raw responses, credentials or captured identifier values in results.

`POST /api/worker/runs/{id}/progress` records bounded step progress using the existing lease token and frozen scenario snapshot. It cannot approve a plan, renew a lease or change a run’s final outcome. A cancelled or expired worker has a short window to record final cleanup only. Existing run/scenario JSON storage carries the ordered steps; no separate workflow service or migration is needed.
