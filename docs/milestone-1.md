# Milestone 1: a repeatable correctness proof

Build a runnable local pilot, not a production multi-tenant SaaS. One operator configures an API token, PostgreSQL, a worker token, and permitted target origins. The browser worker runs on a trusted machine against the controlled sample. Production tenant accounts, encrypted customer secret storage, hosted sandboxing, billing, and subscription login are later milestones.

## Structure and ownership

- `cmd/server`, `internal/qa`, `internal/httpapi`, `migrations`: Go API, PostgreSQL persistence, explicit scenario/run rules. Backend agent owns these plus `go.mod` and `go.sum` initially.
- `worker`: TypeScript Playwright runner, validation, worker polling, screenshots/video. Browser agent owns this.
- `sample`: deterministic SaaS fixture with login, revenue, orders and an injected calculation defect. Browser agent owns this.
- `web`: React/TypeScript workspace for setup, scenario coverage, running and reviewing checks. Web agent owns this.
- `cmd/qa`, `scripts`, root configuration, CI, integration documentation: lead owns these.

Use module `github.com/Chibuoyimm/qa-agent`. Prefer domain packages and direct calls. A PostgreSQL-backed lease queue belongs with the run operations; do not build a generic workflow framework. Root Go version and Node toolchain are chosen from locally installed stable releases and pinned in CI. Use supported stable dependency versions and lockfiles.

## HTTP contract (v1)

All API endpoints except `GET /healthz` require `Authorization: Bearer <QA_API_TOKEN>`. Worker endpoints require a separate `QA_WORKER_TOKEN`. No permissive CORS; the web development server proxies `/api` to the API. JSON errors are `{ "error": "human-readable message" }`. Invalid input is 400, missing record 404, invalid state 409. Collections are JSON arrays, including empty collections. IDs are opaque strings; timestamps are RFC3339 UTC. Never send secrets in project/run responses.

- `GET /api/projects`: `Project[]`.
- `POST /api/projects`: `{name, base_url}` -> 201 `Project`.
- `GET /api/projects/{id}/scenarios`: `Scenario[]`.
- `POST /api/projects/{id}/scenarios`: scenario input without server fields -> 201 `Scenario`. Store immutable revisions: editing/approval workflows follow later. Only approved scenarios are eligible for runs; create explicitly with `approved: true` after human review in this pilot.
- `POST /api/projects/{id}/runs`: `{scenario_ids: string[], mode: "advisory"|"blocking"}` -> 202 `Run`. Nonempty unique IDs, all approved and belonging to the project. Snapshot selected scenarios and target URL atomically. No test assertion can change inside a run.
- `GET /api/projects/{id}/runs`: `Run[]`, newest first.
- `GET /api/runs/{id}`: `Run`.
- `POST /api/runs/{id}/cancel`: pending or running -> `Run` with cancelled state. Late worker results must be rejected.
- `POST /api/worker/claim`: `{worker_id: string}` -> 200 `{run: Run, lease_token: string, lease_expires_at: string}` or 204. Claim oldest pending run atomically with a 60-second lease. An expired running lease becomes `error`, never a pass or implicit retry. This pilot does not automatically re-execute side effects after worker loss.
- `POST /api/worker/runs/{id}/heartbeat`: `{lease_token}` -> 200 `{lease_expires_at}`; extends a live lease by 60 seconds. Expired/replaced/cancelled leases reject with 409.
- `POST /api/worker/runs/{id}/complete`: `{lease_token, results: ScenarioResult[]}` -> 200 `Run`. Require exactly one result for every snapshotted scenario, unique IDs, supported statuses and bounded strings. Derive overall state server-side. Do not trust a submitted overall pass.

`Project`: `{id, name, base_url, created_at}`. Validate HTTP(S) base URL against exact configured `QA_ALLOWED_ORIGINS` (comma-separated origins); reject credentials, query, fragment, non-root paths. Same allowlist must apply at execution. Local sample origins are explicitly allowed only for local development; this is not a hosted arbitrary-URL crawler.

`Scenario`: `{id, project_id, name, description, expected_outcome, approved, steps: Step[], created_at}`. Limit name 200, description/expected_outcome 4000, steps 1–50. Human-readable expected outcome is required. Author-approved machine assertions must contain at least one `assert_text` or `assert_visible` step. Validate all action-specific fields; never accept arbitrary JavaScript.

`Step`: `{action: "navigate"|"fill"|"click"|"assert_text"|"assert_visible", path?: string, test_id?: string, value?: string, secret_env?: string}`. Navigate uses a same-origin absolute path beginning with one `/` (not `//`); other actions use a required `test_id`, resolved with Playwright's `getByTestId`. Fill uses exactly one of `value` or `secret_env`; `secret_env` must begin `QA_TEST_`, and references a worker-local environment value. Assert text is exact text, not a substring. Reject extraneous fields inappropriate to each action. These are pilot selectors, not a promise that all apps have test IDs.

`Run`: `{id, project_id, base_url, mode, status, gate, scenarios: Scenario[], results: ScenarioResult[], created_at, started_at?: string, finished_at?: string}`.

`status`: `queued`, `running`, `passed`, `failed`, `blocked`, `error`, `cancelled`.

`gate`: `pending`, `pass`, `warn`, `fail`. A completed all-passed run is `pass`; a non-passing terminal run is `warn` in advisory and `fail` in blocking. Nonterminal runs are pending. `blocked` means a missing prerequisite, `failed` means an assertion mismatch, `error` means execution/infrastructure trouble. Terminal precedence for mixed results: error, blocked, failed, passed. Never label an incomplete run passed.

`ScenarioResult`: `{scenario_id, status: "passed"|"failed"|"blocked"|"error", message, duration_ms, artifacts: Artifact[]}`. `Artifact`: `{kind: "screenshot"|"video", path: string}`. Relative artifact paths are metadata only, not URLs served by the API in this milestone. Reject absolute paths, URL schemes and traversal. Worker writes artifacts beneath its configured `QA_ARTIFACT_DIR`; use run/scenario IDs and avoid leaking credentials in result messages.

## Browser execution

Each scenario gets a fresh browser context. Exact origin allowlist applies to navigation and network requests, including redirects. Permit only configured HTTP(S) origins and internal browser data/blob resources where needed. Record screenshots and video. Close contexts and browsers in `finally`. Heartbeat while executing; stop work if lease ownership is lost or run is cancelled. Bound scenario/run duration; worker shutdown cancels current browser actions and closes resources. Missing referenced login secrets are blocked. Assertions fail; browser/network/config faults are errors. Preserve every requested scenario in the completion report, even when the worker cannot execute it.

## Controlled sample and acceptance

Use sample port 4174 with an explicitly synthetic test login. Fixed seeded orders: paid 150000, refund 10000, cancelled 90000. Correct net revenue is 140000; a configurable defect returns 230000. Include test IDs for login email/password/submit, revenue, orders, signout, and any state-changing flow. Define the exact IDs in `sample/README.md` and send them to the lead/web agent.

Prove: approved scenarios can be run through the API and worker; healthy sample passes; faulty total fails even if UI matches its API; missing secret blocks; an unapproved scenario cannot run; lease expiry/cancellation cannot become success; results survive server restart. CI CLI polls with a timeout, prints plain-language findings, and exits 0 for pass/advisory warning, 1 for blocking failure, 2 for transport/config/timeout failures. Root integration script must exercise a real browser and PostgreSQL.

## Web scope

Provide project setup, a selected project's scenario list with expected outcomes and approval state, a guided JSON scenario import/editor, mode selection, run launch, polling results, and explicit coverage counts by approved/unapproved and pass/fail/blocked/error. Token is entered by the operator and kept in memory, never persisted to localStorage. Explain that coverage describes configured scenarios, not every possible app behaviour. Give clear loading, empty, error and incomplete states. Show evidence paths as local worker files, without inventing playable remote links.

## Following milestones

After the deterministic proof: repository context ingestion; model-generated scenario proposals with human approval; managed AI and BYOK through the same provider operation; richer selectors and state setup; secure customer credentials; deployment identity; hosted execution isolation; then verified subscription integrations. Do not call model-free execution an AI discovery capability.
