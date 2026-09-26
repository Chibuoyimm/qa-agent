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

`make integration` builds the API and CLI, starts a disposable PostgreSQL container and healthy/faulty sample servers on temporary ports, and runs real Chromium checks. It verifies release decisions, missing secrets, approval rejection, cancellation, persistence across API restart, CLI exit codes, and the web-to-worker flow. It removes the processes/container it creates and retains synthetic evidence in ignored `artifacts/integration-*` directories. Database unit/integration tests use temporary schemas and remove them afterward.

The GitHub Actions workflow runs the same checks on pushes and pull requests. No paid model calls or customer credentials are required. Browser evidence includes screenshots and videos; the UI currently displays worker-local paths rather than hosting those files.

## Structure

| Path | Responsibility |
|---|---|
| `cmd/server` | Process configuration and HTTP server lifecycle |
| `cmd/qa` | Release-pipeline client |
| `internal/qa` | Typed scenarios, validation, persistence and run transitions |
| `internal/httpapi` | Authenticated HTTP boundary |
| `internal/planner` | OpenAI proposal generation and output validation |
| `migrations` | Embedded pilot schema; applied idempotently at startup |
| `worker` | Playwright execution, leases and local evidence |
| `web` | Project, coverage, scenario and result workspace |
| `sample` | Synthetic target, fixed business data and defect switch |
| `scripts` | Sample setup and full integration proof |

## AI proposals

Configure `QA_OPENAI_MODELS` with the exact Responses-compatible model IDs you intend to use and restart the API. For managed credentials, also configure `QA_OPENAI_API_KEY` on the server. With models configured, BYOK is available using a key supplied transiently in the web form. Both paths use the same provider implementation; this pilot does not implement credit purchases or customer billing.

In the scenario workspace, choose **Ask AI to propose**, describe the testing request, and paste relevant application context. [Sample app context](docs/sample-app-context.md) provides a synthetic example. The form asks for explicit consent to send that material to OpenAI. It does not automatically upload repositories or credentials. Draft scenarios, questions and assumptions appear for review, and no generated scenario is approved or executed automatically. Keys stay out of persistence and logs; use HTTPS if accessing the API beyond localhost.

Proposal calls have a 90-second deadline, a 6000-output-token cap, and a two-call concurrency limit. Provider errors, refusals and incomplete/invalid output are failures, not fabricated drafts. No automatic model retry occurs. See [the proposal contract](docs/ai-proposals.md) for details. Automated provider tests use a fake HTTP transport; the integration UI check simulates generation and saves through the real database. Live model quality and account/model access require a separately configured provider key and are not established by those tests.

Repository synchronisation, autonomous exploration, hosted browser isolation, encrypted customer secret storage, tenant accounts, role-rich fixtures, billing, and subscription login remain separate work. The browser pilot supports one main page and `data-testid` selectors; iframe requests, popups and WebSockets return explicit errors. Configured-scenario counts are not claims of complete application coverage.

Stop local development processes with Ctrl+C and run `docker compose down` when finished. The Compose database volume is retained for your next session.
