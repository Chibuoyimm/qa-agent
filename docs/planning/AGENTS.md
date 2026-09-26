# QA tool development instructions

These instructions apply to the QA product described in this directory. The project is currently in planning; do not begin application implementation until the user requests it.

## Go engineering standard

The user selected [pipethedev's Go Codebase Cleanup guide](references/pipethedev-go-deslop.md) to guide this build. Read the full guide before the first Go implementation or substantive Go review in a session. Apply it when writing new code as well as when refactoring existing code. The unchanged source and its pinned revision/hash are recorded in [source metadata](references/pipethedev-go-deslop.source.json).

Use concrete types, explicit business rules, straightforward control flow, and the minimum meaningful layering. Validate external inputs at their boundaries. Prefer explicit dependency wiring and ordinary Go over speculative frameworks, factories, generic utilities, reflection, or pass-through services. Define small interfaces at the consumer only when there is a concrete need. Keep failures visible; never convert invalid input or failed work into silent success.

Treat the guide's search patterns as review prompts, not automatic bans. Preserve observable behaviour, real optional states, resource cleanup, concurrency correctness, security checks, and useful abstractions. Explain an exception through its concrete requirement, not through a claim that the project might need it someday.

## Applying the guide to this product

- Start with the agreed Go backend and separate TypeScript/Playwright browser workers. Organise Go packages around implemented capabilities, without creating an empty framework of layers in advance.
- Give scenario definitions, expected outcomes, run states, worker messages, and provider results concrete contracts. Decode and validate HTTP, webhook, queue, model, and browser-worker input at the relevant boundary. Treat model output as untrusted input.
- Use a narrow provider interface for the actual model operations the product needs. Multiple providers are a real requirement; a universal plugin framework is not. Managed AI and BYOK should use the same provider implementation with different credential sources and billing attribution.
- Keep tenant authorisation, secret access, permitted test actions, and run-state transitions explicit. Input validation does not replace authorisation at each protected operation. A failed or blocked required check cannot silently become a pass.
- Use durable background execution for real long-running work. Define cancellation, retry, idempotency, concurrency limits, and cleanup from concrete run requirements; do not launch unmanaged goroutines or invent a workflow engine by default.
- Keep approved pipeline assertions fixed during a run. A retry or proposed repair must not weaken the expected business outcome to obtain a pass.
- Test business outcomes and meaningful failure cases. Use real components where practical and small substitutes at expensive external boundaries; avoid mock-heavy internal architectures.

For Go changes, format changed code and run the relevant tests and static analysis. Use `go test ./...` and `go vet ./...` when applicable to the repository; use `staticcheck ./...` if it is part of the configured toolchain. Exercise race detection when changes introduce shared state or concurrency. Report what was actually verified.

## Continuity into implementation

When the application repository is created, carry these instructions, the source guide, and its metadata into that repository and reference them from its root `AGENTS.md`, adjusting relative paths. Do not assume instructions in this planning directory apply automatically to a separate checkout. Keep the vendored source unchanged; review upstream updates explicitly before adopting them.

Use [build readiness](build-readiness.md) for accepted product decisions and outstanding questions. The Go guide governs engineering style; it does not override product requirements or authorise unrelated cleanup.
