# ChatGPT subscription setup and verification

The local QA workspace can use a connected ChatGPT plan to generate draft scenarios. It uses OpenAI's **Sign in with ChatGPT** flow and the public Responses API. Repository snapshots, browser observations, expected-outcome validation, human approval, and deterministic browser execution follow the existing proposal workflow.

This integration is disabled by default. It supports a trusted operator on a local machine; it does not turn ChatGPT sign-in into application authentication or provide customer accounts. The documented open-source/personal local route needs no partner API key or client secret. Paid or remotely hosted offerings require OpenAI partner access before being offered to users. See [availability and the integration guide](https://developers.openai.com/cookbook/articles/sign-in-with-chatgpt).

## Enable the local connection

Follow the README's normal local setup. Add this to your private `.env`, load that environment in the API terminal, and restart `make server`:

```sh
QA_CHATGPT_ENABLED=true
```

Keep `QA_LISTEN_ADDR` on a loopback IP. The API refuses to enable this connection on a non-loopback address. No `QA_OPENAI_API_KEY` or `QA_OPENAI_MODELS` value is required for ChatGPT plan access. Existing managed and API-key modes keep their own settings and model allowlist.

Credentials default to the operating system's user configuration directory, under `qa-agent/chatgpt` (on macOS, `~/Library/Application Support/qa-agent/chatgpt`). An optional absolute `QA_CHATGPT_STORAGE_DIR` selects another protected local directory outside the repository. The directory must have owner-only permissions. Never commit or share its contents. One API process holds an exclusive lock on that directory; a second process must use another installation directory or wait for the first to stop.

Open **Scenarios → Ask AI to propose**, choose **ChatGPT subscription**, and click **Continue with ChatGPT**. Finish authentication and authorize ChatGPT plan usage in the browser tab. The callback listener is bound to `127.0.0.1` on a temporary port and expires after ten minutes. The QA panel checks completion and loads models available to the connected account. A successful identity login without plan permission cannot generate proposals.

Select the account and model, review the application context and any selected source snapshots or discovery observations, then consent and generate. Each request is pinned to the selected account. Changing the account or model clears consent and old drafts. Returned scenarios remain unapproved; review their business expectations before saving and approving them.

Use **Manage usage** to open [ChatGPT Settings → Usage](https://chatgpt.com/settings/usage). A quota or permission failure produces an error. The app does not automatically switch to a paid API key or another provider.

## Connection lifecycle and credentials

The runtime creates a stable installation host ID and uses PKCE, one-time state, and a nonce for each sign-in. It retains the issued client ID, verifies the signed ID token's issuer, audience, expiry, and nonce against OpenAI's JWKS, and checks granted plan-usage permission. Separate account registrations remain separate even when emails match.

Access, refresh, and retained ID tokens stay in protected runtime files; status responses contain only display information and connection state. The browser receives no access or refresh token, and none enters the project database, browser storage, workers, model context, logs, or committed evidence. Retained ID tokens may appear only in the OpenAI authorization URL as a documented returning-login hint; do not copy or log that URL.

Token renewal is serialized and replacements are written atomically with owner-only file permissions. Account switching, disconnecting, and server shutdown cancel outstanding account-bound requests. Disconnect attempts remote revocation and clears local tokens while preserving the installation and account registration. If remote revocation cannot be confirmed, the panel explains how to disconnect the app in ChatGPT Settings.

## Subscription request boundaries

Subscription calls use `POST https://api.openai.com/v1/responses`, an account-authorized model, an input-message array, `store:false`, and `stream:true`. Unlike the API-key path, this preview does not accept `max_output_tokens`, so subscription requests omit it. The local 90-second deadline, two-request concurrency limit, and 2 MiB stream bound still apply. No tools or automatic retries are requested.

The runtime accepts a proposal only after a complete `response.completed` event. Refusal, incomplete output, interrupted streams, usage errors after text begins, invalid JSON, and invalid browser actions fail. Partial text cannot become a draft or a release pass. Structured output does not verify business truth; human approval remains required.

Sources: [registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions), [models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference), [preview requirements](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).

## Verification

Automated Go tests exercise the real loopback callback with signed synthetic ID tokens and substituted external HTTP responses. They cover state/PKCE/identity checks, registration persistence, renewal, credential permissions, account isolation, cancellation, and stream completion/error boundaries. API tests verify operator authorization and subscription availability independently of API keys.

`make integration` exercises the real web workspace, API, PostgreSQL, and browser worker. The subscription UI uses synthetic account and provider responses to check connection/cancellation, model and account consent, quota errors, draft review, and disconnect; draft persistence uses the real database. It makes no paid model request and does not prove real account admission or live model/schema compatibility.

For the live acceptance check, sign in with an eligible account and use synthetic sample context to generate proposals. Review and approve meaningful checks, then execute them against the healthy and faulty sample builds. Record completed inference and observed browser outcomes separately from sign-in success. Keep credentials and raw authorization URLs out of evidence.
