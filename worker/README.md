# Browser worker

This is the local Playwright worker for milestone 1. It polls the Go API for approved run snapshots, runs each scenario in a fresh Chromium context, heartbeats its lease, and submits one result per scenario. It executes only the fixed `navigate`, `fill`, `click`, `assert_text`, and `assert_visible` actions. There is no arbitrary JavaScript action.

Install and start:

```sh
npm ci
npx playwright install chromium
QA_API_BASE_URL=http://127.0.0.1:8080 \
QA_WORKER_TOKEN=replace-with-local-worker-token \
QA_ALLOWED_ORIGINS=http://127.0.0.1:4174 \
QA_ARTIFACT_DIR=../artifacts \
QA_TEST_EMAIL=demo@example.test \
QA_TEST_PASSWORD=pass1234 \
npm start
```

`QA_API_BASE_URL` is the Go API origin. `QA_ALLOWED_ORIGINS` is a comma-separated list of exact HTTP(S) origins and must match the API's permitted target origins. `QA_ARTIFACT_DIR` must be writable by the worker and should stay outside source control. `QA_WORKER_ID` is optional. Referenced `QA_TEST_*` values live only in the worker environment; missing values yield `blocked` results. Test IDs and scenario examples are in [the sample README](../sample/README.md).

Screenshots and videos are saved beneath the artifact directory using hashed run and scenario IDs. The API receives relative metadata paths, not file contents or served URLs. Each result has a bounded plain-language status: `passed`, `failed` for an assertion mismatch, `blocked` for a missing secret, or `error` for browser/network faults. Browser exception strings and credential values are not sent to the API.

Chromium's request interception checks each request and native redirect hop against the origin allowlist before network contact. Native redirects retain the final browser URL and resolve relative resources normally. WebSockets and popups are rejected in this pilot. Each context closes after its scenario, and the browser closes after the run. SIGINT/SIGTERM stops current actions. A rejected heartbeat aborts work; the worker does not submit a result after it loses the lease.

Run `npm run check` for TypeScript validation and `npm test` for real-browser tests against healthy and faulty in-process samples. The tests also verify missing-secret handling, evidence files, and disallowed origin and WebSocket behavior.
