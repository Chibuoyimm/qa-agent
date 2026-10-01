# QA Agent

A QA workspace for understandable, evidence-backed browser testing of SaaS applications.

Go controls projects, approved scenarios, and durable runs. TypeScript/Playwright executes browser checks. A web workspace explains coverage, findings, and missing checks.

This is an early local pilot for a trusted operator and controlled targets, not a production multi-tenant service. The first proof checks a sample dashboard against known paid, refunded, and cancelled orders. The deliberately faulty build displays the same wrong value as its API; independent expectations still catch it.

Read [the engineering instructions](AGENTS.md), [product research](docs/planning/research-and-product-plan.md), and [the first milestone contract](docs/milestone-1.md).

## Run locally

Prerequisites: Go 1.27.1 (CI version), Node 24.10.0, npm, Docker with Compose. The Go module declares the minimum supported Go language version.

```sh
cp .env.example .env
set -a
. ./.env
set +a
make install
make db
```

In four terminals, load `.env` as above and run each process:

```sh
make server  # Go API at http://127.0.0.1:8080
make sample  # controlled target at http://127.0.0.1:4174
make worker  # executes leased scenarios and saves browser evidence
make web     # workspace at http://127.0.0.1:5173
```

Run `node scripts/seed.mjs` once to create a sample project and its six reviewed scenarios. Each invocation creates a new project; it does not reset existing projects. The script prints a usable CLI command. Open the web workspace and enter the local `QA_API_TOKEN`. It remains in tab memory and clears on reload.

The `.env.example` database and sample login values are explicitly synthetic local fixtures. Use distinct random API and worker tokens for a shared development machine. Never commit `.env`, customer credentials, browser state, or generated evidence. Target origins must be explicitly configured in both API and worker `QA_ALLOWED_ORIGINS`.

To see the revenue defect, stop the sample and restart it with `QA_SAMPLE_DEFECT=1 make sample`. The approved expectation remains 140000; the broken dashboard displays 230000. The assertion fails without changing its expectation.

## Release pipelines

Use `bin/qa release --manifest release.json --timeout 10m --json` to wait for a deployment, import its exact frontend/backend revisions, run approved checks, and return the gate. The **Releases** page shows the deployment URL, commits, frozen expectations, and findings. A stable deployment key lets pipeline retries resume the original run without executing actions again. See [the manifest, API, and GitHub Actions setup](docs/deployment-qa.md), including a reusable workflow example.

For a standalone run against the project's default URL:

```sh
go build -o bin/qa ./cmd/qa
QA_API_BASE_URL=https://your-private-qa-api.example \
  bin/qa run --project PROJECT_ID --scenarios SCENARIO_ID,SCENARIO_ID \
  --mode blocking --timeout 5m
```

Supply `QA_API_TOKEN` through your pipeline's secret environment. Advisory is the default mode. Exit codes:

| Code | Meaning |
|---|---|
| 0 | All selected checks passed, or an advisory run completed with a warning |
| 1 | A blocking run failed, was blocked, encountered an execution error, or was cancelled |
| 2 | Configuration, transport, timeout, or an inconsistent/incomplete API decision |

The CLI does not cancel a remote run merely because the client times out. It prints the run ID so the operator can inspect or cancel it. Approved assertions are snapshotted when a run is created. Expired worker leases become errors; the pilot does not automatically repeat potentially state-changing actions.

## Verify the implementation

```sh
make check
TEST_DATABASE_URL="$DATABASE_URL" go test -race ./...
make integration
```

`QA_PROOF_GITHUB=1 make integration` additionally verifies the complete release command with live public GitHub imports. Normal CI seeds the external repository snapshots locally and verifies the real release API, database, browser worker, and CLI resume path; command tests cover new-import/readiness boundaries with HTTP fixtures.

`make integration` builds the API and CLI, starts a disposable PostgreSQL container and healthy/faulty sample servers on temporary ports, and runs real Chromium checks. It verifies deployment records and gates, retry reuse, run-specific targets, saved source review, missing secrets, approval rejection, cancellation, persistence across API restart, CLI exit codes, and the web-to-worker flow. It removes the processes/container it creates and retains synthetic evidence in ignored `artifacts/integration-*` directories. Database unit/integration tests use temporary schemas and remove them afterward.

The GitHub Actions workflow runs the same checks on pushes and pull requests. No paid model calls or customer credentials are required. Browser evidence includes screenshots and videos; the UI currently displays worker-local paths rather than hosting those files.

## Structure

| Path | Responsibility |
|---|---|
| `cmd/server` | Process configuration and HTTP server lifecycle |
| `cmd/qa` | Release-pipeline client |
| `internal/qa` | Typed scenarios, validation, persistence and run transitions |
| `internal/httpapi` | Authenticated HTTP boundary |
| `internal/planner` | OpenAI, Anthropic and Gemini proposal generation and output validation |
| `migrations` | Embedded pilot schema; applied idempotently at startup |
| `worker` | Playwright execution, leases and local evidence |
| `web` | Project, coverage, scenario and result workspace |
| `sample` | Synthetic target, fixed business data and defect switch |
| `scripts` | Sample setup and full integration proof |

## Repository context and browser discovery

The **Repository** page imports selected files from a public or private GitHub repository into an immutable snapshot. Choose a frontend/backend role, a branch/tag/ref, and exact file paths. Private repository tokens are used for that request only. Each snapshot records the resolved commit SHA and a content hash; the complete content is reviewable before model sharing. Re-sync creates a new snapshot. The deployed app may differ from that source revision, so it remains evidence rather than an expected-outcome oracle. See [repository limits](docs/repository-context.md).

The **Discover** page schedules a bounded browser visit independently of model access. Choose a starting path, a one-to-five-page limit, and optionally an approved setup scenario for login. The worker runs that exact setup, then follows eligible same-origin links while blocking write requests. It collects visible controls, headings and text; it does not collect form values, cookies or browser storage. Observations persist and can be reviewed and included in a proposal. A completed discovery is not a passed test. [Discovery boundaries and API](docs/discovery.md) explain limitations, cancellation and leases.

Pipelines can schedule discovery as well:

```sh
bin/qa discover --project PROJECT_ID --start-path /dashboard \
  --setup-scenario APPROVED_LOGIN_SCENARIO_ID --max-pages 3 --json
```

`--json` writes the terminal discovery object to stdout and progress to stderr. Exit 0 means observation completed, 1 means error/cancellation, and 2 means client or transport failure. This command does not change approved checks or decide a release gate.

## AI proposals

For local ChatGPT subscription access, set `QA_CHATGPT_ENABLED=true`, restart the API, and choose **ChatGPT subscription → Continue with ChatGPT** in the proposal panel. Models come from the connected account; no API key is needed. See [connection setup, limits, and verification](docs/chatgpt-subscription.md).

Configure the model allowlist for each provider you want to use: `QA_OPENAI_MODELS`, `QA_ANTHROPIC_MODELS`, or `QA_GEMINI_MODELS`. Use exact model IDs supporting the documented structured-output format and restart the API. Choose the provider and model in the form, then supply an API key for that request. Anthropic also has an optional workspace ID field for keys that require it. Server-managed access uses the corresponding `QA_OPENAI_API_KEY`, `QA_ANTHROPIC_API_KEY`, or `QA_GEMINI_API_KEY`; a managed multi-workspace Anthropic key also uses `QA_ANTHROPIC_WORKSPACE_ID`. Both paths use the same provider implementation; this pilot does not implement credit purchases or customer billing.

In the scenario workspace, choose **Ask AI to propose**, describe the testing request, and paste relevant application context. [Sample app context](docs/sample-app-context.md) provides a synthetic example. The form asks for explicit consent to send that material to the selected provider. Only explicitly selected repository snapshots and discovery observations are included; credentials are not model context. Draft scenarios, questions and assumptions appear for review, and no generated scenario is approved or executed automatically. Keys stay out of persistence and logs; use HTTPS if accessing the API beyond localhost.

Proposal calls have a 90-second deadline and a two-call concurrency limit. API-key calls have a 6000-output-token cap; the subscription preview omits that unsupported field and bounds the response stream to 2 MiB. Provider errors, refusals and incomplete/invalid output are failures, not fabricated drafts. No automatic model retry occurs. See [the proposal contract](docs/ai-proposals.md) for details. Automated provider tests use a fake HTTP transport; the integration UI check simulates generation and saves through the real database. Live model quality and account/model access require a connected eligible ChatGPT account or a configured provider key, and are not established by those tests.

Automatic repository webhooks, broader interactive exploration, hosted browser isolation, encrypted customer secret storage, tenant accounts, role-rich fixtures, billing, and hosted commercial subscription access remain separate work. The browser pilot supports one main page and `data-testid` selectors; iframe requests, popups and WebSockets return explicit errors. Configured-scenario counts are not claims of complete application coverage.

Stop local development processes with Ctrl+C and run `docker compose down` when finished. The Compose database volume is retained for your next session.
