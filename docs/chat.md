# Testing conversations

Open **Chat** inside a project to request checks for a feature or user journey. Select an AI connection under **App context & AI connection**; existing ChatGPT, OpenCode Go, managed and transient API-key access use the same generation rules as the scenario workspace. A connected, sharing-enabled ChatGPT account is selected initially when available.

Messages can begin without application context. In that case the model is instructed to ask for missing requirements, paths and test IDs. Supply answers in the next message, or add requirements to the context field. Reviewed repository snapshots and browser discovery observations can be selected under **Review source context**. Discovery remains a separate bounded browser operation; sending a drafting message does not browse the target.

The model returns questions, assumptions and unapproved draft checks. **Review check** opens the existing editor with approval off. Saving an approved check requires confirming its expected outcome and assertions. A saved approved check can run directly from the conversation. Original draft messages remain draft evidence; saving a check does not rewrite the original model response. All approved checks also remain available in Scenarios.

## Commands and results

**Run all approved checks**, “run all checks”, “run a full check” and “full check” run the project's approved scenarios without an AI call or model-sharing consent. The button and composer label make execution explicit. A full check covers configured approved scenarios, not exhaustive app coverage. Empty coverage and more than 50 approved checks produce an error rather than a partial full check.

“Rerun failed checks” uses the latest run linked in this conversation. A result card's **Rerun failed checks** button selects that specific run. Only failed assertions are repeated; blocked, cancelled and infrastructure-error scenarios are not silently treated as failed assertions. The source run must have finished and belong to the same project. Expected outcomes remain unchanged. Other natural-language requests go through drafting and cannot execute model-proposed actions automatically.

Choose advisory or blocking mode before launching. Result cards poll the real run, show findings and expected outcomes, support cancellation and link to the complete report. A blocked or incomplete run is never described as passed. Losing a launch response does not prove that no run was queued; refresh the conversation to check its stored run reference.

## Storage and sharing

Each project has one durable conversation in PostgreSQL. Messages, validated proposals and run references survive browser reload and API restart. This is still the trusted operator-token pilot, not individual user accounts or tenant isolation. Worker tokens cannot access chat endpoints. The browser stores neither the operator token nor chat credentials in local/session storage.

App context, source file contents, transient keys and workspace headers are not stored in chat. OpenCode and ChatGPT retain their existing protected connection storage. Messages themselves are saved, so never paste credentials into the message field. Context/source selection is held in the mounted composer and must be supplied again after navigation or reload.

Explicit model consent includes the current message, application context, selected reviewed sources and up to eight successful drafting exchanges from the latest 20 turns. History is limited to 12,000 bytes; whole older/oversized exchanges are omitted. The assembled context must fit the existing 60,000-byte limit. For older or large discussions, include relevant details in the current message. Run results and arbitrary older context are not automatically forwarded to the model. Previous proposals are labelled unapproved evidence, not verified business expectations.

## API

- `GET /api/projects/{id}/chat` returns `{turns,has_older}`, oldest-to-newest within the latest 20 turns. `?before=<positive sequence>` loads an earlier page.
- `POST /api/projects/{id}/chat` accepts the existing proposal input plus `request_id` (32 lowercase hexadecimal characters). It returns one persisted turn with a validated proposal. The existing 128 KiB body, two provider-call slots, 90-second model deadline and strict proposal validation apply; HTTP requests have a 95-second deadline. A request reserves one pending drafting turn per project without holding a database transaction during inference. Failures/cancellation close the turn as an error using a fresh bounded cleanup context. Pending turns from a process interruption expire after 100 seconds when chat is read or a new request starts.
- `POST /api/projects/{id}/chat/runs` accepts `{request_id,prompt,action,mode,scenario_ids?,source_run_id?}`. Actions are `all_approved`, `selected` and `rerun_failed`. The run and conversation turn are committed in one transaction. A completed request with the same ID and JSON fingerprint returns its original turn; changed inputs conflict. The client does not retry automatically. There is no model-controlled run action.

## Verification

Go tests exercise real PostgreSQL persistence, pagination, pending recovery, concurrent replay producing one run, approval and project boundaries, failed-only reruns and unchanged expectations. HTTP tests use a synthetic provider transport to verify follow-up history, transient-key exclusion, consent, quota failure and cancellation without browser execution.

The browser harness uses synthetic model replies for chat questions and drafts. Saving/reviewing checks, execution commands, durable run conversation, healthy/blocked/faulty browser runs, failed-only reruns and complete reports use the real API, PostgreSQL and Playwright worker. Screenshots cover desktop/mobile. Actual inference through a user's live model connection remains a separate check; no OpenCode subscription key is assumed.

On 3 October 2026, a live smoke test in the retained **Chat demo** project used the connected ChatGPT subscription and `gpt-5.6-sol`. It produced one unapproved revenue draft using synthetic requirements. Its secret references, paths, selectors and independent 140000 expectation were reviewed before explicit approval. Launching that check from chat completed through the real local API, PostgreSQL and Playwright worker with a passed result. No OpenCode key or inference was used.
