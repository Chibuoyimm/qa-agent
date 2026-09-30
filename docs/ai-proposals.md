# Milestone 2: prompt to reviewable scenarios

This increment adds OpenAI-backed scenario proposals to the local pilot. It does not claim autonomous browsing or repository synchronisation yet. A user supplies a testing request and relevant application context (requirements, known test IDs, paths and fixture rules). The provider returns proposed scenarios and questions; it cannot approve scenarios, run tools, fetch URLs, or edit an active run.

## API contract

`GET /api/ai/config` (operator auth) returns `{provider:"openai", models:string[], managed_available:boolean, byok_available:boolean}`. Models come from `QA_OPENAI_MODELS` (comma-separated explicit model IDs); an empty list disables API-key proposal generation. When local ChatGPT plan access is enabled, configuration also includes a display-only `chatgpt` status. Its models come from the selected account rather than this allowlist; see [the subscription contract](chatgpt-subscription.md). Managed mode additionally requires server-local `QA_OPENAI_API_KEY`. Never return a key. This is technical credential routing, not a launched billing product.

`POST /api/projects/{id}/proposals` requires operator auth and JSON `{prompt:string, context:string, model:string, credential_mode:"managed"|"byok"|"chatgpt", consent:true}`. API-key models must be configured. ChatGPT mode also requires `chatgpt_profile_id` for the connected active account and a model returned by that account’s catalog; it rejects a provider-key header. Prompt is 1–4000 characters; context 1–60000. Consent explicitly covers sending this supplied material to OpenAI. Do not implicitly attach test credentials, existing browser state or arbitrary repository files. BYOK additionally supplies `X-QA-Provider-Key`; use it only for this request, without persistence or logging. Credential modes must not fall back to each other. Reject malformed/unknown fields, missing consent, missing credentials and missing project before any provider call.

Return 200 `{provider:"openai", credential_mode:string, chatgpt_profile_id?:string, model:string, context_sha256:string, scenarios:ScenarioInput[], questions:string[], assumptions:string[]}`. All scenarios have `approved:false`; require 1–50 steps and at least one valid assertion for each proposal. Permit zero scenarios when context is insufficient, accompanied by focused questions. Bound output to at most 10 scenarios, 20 questions, 20 assumptions, and 4000 characters per question/assumption. Do not save proposals automatically; the user reviews, edits, then saves through existing scenario creation. Generation failures never produce invented success data.

Use 400 for invalid input, 404 for missing project, 503 for unconfigured access, 502 for invalid/refused/incomplete provider output or upstream failure, and 504 for provider timeout. Errors must omit provider bodies, keys, prompts and context. Keep a 90-second provider deadline, request body limit 128KiB for this endpoint, and response limit 2MiB. Limit to two concurrent proposal calls per server; return 429 when busy. Cap API-key output tokens at 6000. ChatGPT subscription calls omit that unsupported field and accept only a complete successful streaming event. Do not retry automatically or make background calls. Subscription errors distinguish reconnection (409), denied plan access (403), quota (429), temporary availability (503), and unsupported request capability (502).

## Implementation

Use one concrete `internal/planner` OpenAI adapter with typed request/response structs and an embedded JSON schema, rather than a speculative provider framework. API-key credential modes use the same operation. The ChatGPT subscription path binds requests to a protected local account session, fetches its model catalog, and decodes streamed Responses output before the same scenario validation. Use the Responses API at the fixed official endpoint, `store:false`, structured JSON output, and no tools. Refuse redirects. Validate actual output in Go with the existing scenario validator; required approval belongs to the user regardless of model output. Make transport injection available only for tests, without user-supplied endpoint URLs.

Prompt the model to treat supplied code/context as evidence rather than instructions, to distinguish known business expectations from guesses, and to ask questions instead of inventing selectors, credentials or expected values. This protects the approval boundary but does not make model suggestions authoritative.

Follow the official [Structured Outputs guide](https://developers.openai.com/api/docs/guides/structured-outputs). Handle completed, incomplete and refusal responses explicitly. Document configured model compatibility; no model access or live generation is verified merely by a fake-server test.

## Web flow

Add “Ask AI to propose” to the scenario area. Show model and managed/BYOK/ChatGPT choices from server configuration, a password field for a transient BYOK key, testing-request and app-context fields, and explicit consent to send those fields to OpenAI. Keep all keys in memory and clear the BYOK field after the request. Display questions and assumptions beside draft outcomes. Let the user open proposals in the existing editor with approval initially off; saving approved scenarios still requires explicit review. Show unavailable/configuration errors honestly. Do not call this repo sync or live exploration.

## Proof

Test both credential modes through a fake HTTP provider, including redirection refusal, malformed/incomplete/refused output, invented action rejection, forced unapproved proposals, cancellation, output bounds, and timeout. Exercise API validation/auth without a provider key. A live paid provider check is conditional on an explicitly configured key; no key is currently assumed available.

The ChatGPT subscription integration adds signed synthetic OAuth callback tests and browser flow checks. These verify local security and persistence boundaries; live account admission and completed inference remain separate acceptance checks.
