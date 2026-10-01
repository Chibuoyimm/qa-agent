# OpenCode Go and Go Plus proposals

This local integration uses OpenCode's own **Go / Go Plus subscription** through `opencode-go`. The existing direct ChatGPT connection remains separate.

## Setup

Install the pinned runtime without package install scripts:

```sh
node scripts/install-opencode.mjs
```

Copy the printed absolute `QA_OPENCODE_BINARY` path into your local `.env`. Set `QA_OPENCODE_ENABLED=true`, load the environment, and restart the QA server. It must listen on a loopback IP. This pilot supports macOS and Linux arm64/x64. Upgrading the runtime requires reviewing its API and running the native contract test first.

Open **Ask AI to propose**, select **OpenCode Go**, and enter the subscription key from your [OpenCode console](https://opencode.ai/auth). Connect once, then choose a model from the Go catalog. “Key connected” means OpenCode stored a credential; it does not prove subscription eligibility, remaining usage, or every model's structured-output compatibility. A real completed proposal establishes access for that request.

For subscription-only usage, disable **Use balance** in the console. OpenCode documents that enabling this option can spend existing Zen credits after Go limits are reached. QA Agent cannot inspect or enforce that remote billing setting. It does not enable it, purchase credits, or switch to Zen, OpenAI, another model, or another provider. Usage follows the selected Go model's plan limits. See [Go's official documentation](https://opencode.ai/docs/go/).

Supply reviewed requirements, paths, selectors and independent expected values. The consent text names OpenCode Go. Review returned drafts in the editor; saved drafts stay unapproved until you explicitly approve them. Unapproved scenarios cannot run.

## Isolation and lifecycle

QA Agent starts an authenticated OpenCode **1.18.34** server on a random loopback port. Its empty workspace, home, configuration, cache and data are separate from your normal OpenCode setup. No existing OpenCode key or session is imported. Default storage is the OS user configuration directory under `qa-agent/opencode-go`; an override must be an absolute protected directory outside Git repositories. The root and runtime directories are owner-only. An exclusive runtime lock prevents concurrent QA servers from using the same connection. Existing unrelated directories are rejected; the root carries a QA ownership marker.

OpenCode persists the Go key in its isolated data directory. The QA database and browser storage never receive it. The key field clears before the one-time local connection request. Only the authenticated operator can access status, connect and disconnect; the browser worker cannot. Public status contains Go model labels and connection state, never keys or the private runtime address/password.

Provider credentials and personal configuration are not inherited in the child environment. Project and Claude configuration, default/external plugins and external skills are disabled. The dedicated agent and session deny files, shell, browsing, MCP, skills and subagents; only OpenCode's internal **StructuredOutput** return tool is allowed. The model receives explicitly reviewed proposal context plus OpenCode's standard system/environment metadata for the empty workspace. Repository files are never attached implicitly. Upstream requests use OpenCode's native client identity and stable `x-opencode-session` header.

Each proposal creates one owned session using the selected Go model. QA Agent requires a completed assistant response from that exact session/provider/model/agent, exactly one completed StructuredOutput call, and valid proposal JSON. The common scenario validator enforces permitted actions, assertions and size limits. Other tool calls, files, partial output and malformed scenarios fail.

Generation has the existing 90-second deadline, 6000-output-token runtime cap and 2 MiB response bound. The local connection handles one operation at a time; the planner's overall two-request bound remains. QA Agent does not retry or switch providers. QA Agent monitors the native session and aborts when it reports a retry, returning a safe quota or availability error. The underlying runtime schedules retries; the native quota regression verifies that QA Agent stops before a second provider request. Structured-output retryCount is zero. Quota, auth, capability and malformed output failures remain errors.

Success, provider failure and cancellation all attempt to abort and delete the owned session with a fresh bounded cleanup context. Cleanup failure is visible and prevents reporting a successful draft. Disconnect removes the Go key and disposes the isolated provider cache so a cached credential cannot continue working. If cache disposal fails, the runtime stops and requires a QA server restart. Shutdown stops the owned process; the protected connection persists for the next launch. On startup, leftover QA proposal sessions from interrupted cleanup are aborted and deleted before the connection is made available.

## Verification

```sh
TEST_OPENCODE_BINARY="/absolute/path/to/opencode" go test -race ./internal/opencode
make integration
```

The native test uses the real pinned executable and a synthetic external model endpoint. It checks native identity/session headers, the single permitted return tool, completed structured output, quota without a second provider request, session deletion, disconnect/cache invalidation and process shutdown. HTTP boundary tests cover wrong models/providers, quota/auth failures, cancellation cleanup and failed deletion. Browser checks cover key clearing, Go model selection, consent invalidation, quota without frontend fallback, disconnect, unapproved persistence, and independently reviewed healthy/faulty execution using the real database and worker.

These tests do **not** establish paid Go account admission, actual model quality, quota availability, or the remote Use balance setting. A live subscription test requires the operator's key in the local connection screen and reviewed synthetic app context. No live Go request has been verified yet.
