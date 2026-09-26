# Build checkpoint

26 September 2026 (Africa/Lagos). Implemented with three GPT-6 Sol agents at high reasoning, with lead review and integration fixes. The public repository is `Chibuoyimm/qa-agent`.

## Working scope

Go API and PostgreSQL persistence; human-approved immutable run snapshots; durable worker leases and cancellation; pipeline CLI; Chromium execution with screenshots/video; six sample scenarios with an independent revenue expectation; responsive scenario/results workspace; commit-pinned repository imports; durable bounded browser discovery with approved setup and CLI support; OpenAI proposal adapter with managed/BYOK credential routing and a human review flow.

The engineering instructions and pinned pipethedev Go guide are part of the repository. Packages own actual capabilities rather than placeholder layers. The API is the authority for approval eligibility, run state and release gates; the model cannot approve scenarios or revise active-run assertions.

## Verification performed locally

- Go tests, vet and race detection against real PostgreSQL, including competing claims, incomplete results, expired leases and cancellation.
- Thirteen worker/contract tests, including all healthy sample journeys, wrong revenue detection, missing-secret preflight, native redirects, relative resources, forbidden network targets, popups, WebSockets and iframe rejection.
- Full API → PostgreSQL → worker → Chromium proof: healthy pass, faulty blocking failure, faulty advisory warning, missing-secret block, unapproved-run rejection, late cancellation result rejection, persistence after API restart, and blocking CLI exit status.
- Real web → API → worker execution, desktop/mobile screenshots, no horizontal mobile page overflow, no browser errors, and no token persistence in browser storage.
- AI proposal UI with simulated generation and real persistence: transient BYOK key clears, questions display, proposals begin unapproved, explicit review is required to approve.
- Fake-provider HTTP tests for both credential modes, output validation, refusal/incomplete responses, deadlines, cancellation, concurrency limits and redirect rejection. **No live paid model call or model-quality evaluation has been performed.**
- Web production build and production-dependency npm audits (no reported vulnerabilities at this checkpoint).

The full proof is reproducible through `make integration`; its disposable processes and database container are removed and synthetic evidence is retained under ignored `artifacts/integration-*`. GitHub Actions runs the repository checks independently; inspect the workflow run for remote status.

## Current boundaries and next work

This is a local single-operator pilot, not a hosted production service. Target origins are explicitly configured. Browser flows use test IDs and one main page; iframe requests, popups and WebSockets fail explicitly. Evidence remains on the worker filesystem. The current fixture has one synthetic account, not a role/tenant matrix.

Next: automatic repository sync and deployment identity; broader interactive discovery; richer fixtures and permission cases; durable proposal provenance and scenario revision lineage; broader selectors; isolated hosted workers and private evidence delivery; encrypted customer credentials and tenant authorisation; model-quality/cost evaluation; then additional providers and separately validated subscription access. Billing is not implemented. Managed/BYOK support here means credential routing through the same operation, not a commercial credit product.

Keep business expectations independent of the current implementation. A larger scenario count or successful page load is not proof of thorough application coverage.

## Continued build verification

The resumed build passed real PostgreSQL race tests and 19 worker/browser tests. A live import from the public `Chibuoyimm/qa-agent` repository resolved a commit and persisted selected files with project scoping. The real browser worker executed approved login/setup, discovered dashboard controls, and saved observations that survived API restart. The final integration also passed discovery through the CLI with JSON output, browser-driven discovery creation and review, and selection of saved observations for simulated AI generation followed by real scenario persistence. Existing healthy/faulty release checks continue to pass/fail as expected. Discovery is an inventory with explicit page/content limits, not a complete flow crawler or proof of business correctness.

## Pause point

Paused at the user's request after the repository-context and bounded-discovery increment. OpenAI remains the only implemented model provider. [The provider adapter research](provider-adapters.md) records the proposed Anthropic and Gemini contracts; incomplete adapter code and automatic repository file selection were set aside before the final commit. No additional background development or usage monitor is left running.
