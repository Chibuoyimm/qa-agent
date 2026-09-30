# Build readiness and remaining decisions

Planning checkpoint, 26 September 2026. This document does not authorise application implementation or deployment. It separates user decisions from proposed engineering defaults and validation work.

**Direction already established**

- Serve small SaaS teams and test web applications through an existing staging or preview URL.
- Connect frontend repositories, with optional backend repositories, to improve discovery and diagnosis.
- Explain journeys, business expectations, coverage gaps, and findings in ordinary language.
- Support prompted testing, repeatable suites, and release-pipeline execution.
- Manage application test accounts, roles, sessions, and secrets.
- Use Go for the backend and TypeScript/Playwright for browser workers; the user accepted this split.
- Include managed AI, model choice, and customer API-key connections. Managed AI and BYOK share the first provider adapter; packaging and price remain proposals.
- Use the documented Sign in with ChatGPT route for OpenAI subscription access in the local pilot, and validate it with a real eligible account. Commercial hosted availability remains dependent on OpenAI partner access. Google and Claude consumer subscription connections are excluded from the recommended launch scope pending explicit permission for the exact offering.

**Defaults accepted by the user, 26 September 2026**

- Let the agent explore and modify designated test data within configured permissions. Ask focused questions about material business-rule ambiguities and keep required pipeline assertions fixed during a run.
- Begin pipeline execution in advisory mode, then enable blocking for approved critical scenarios after establishing reliability.
- Use the Go backend and TypeScript/Playwright worker split described above.
- Share the first provider adapter between managed AI and BYOK. Implement local ChatGPT plan usage separately from API-key credentials; decide hosted launch timing after validating account admission and partner availability.
- Optimise repository analysis for the first pilot application's stack and communicate limits for other frameworks.
- Establish whether the pilot permits its code and screenshots to reach the selected provider, plus any hosting-region and retention requirements. Acceptance of this process is not the pilot's actual data-processing consent or a choice of region/retention period.
- Use pipethedev's Go cleanup guide as the Go implementation and review standard. See [development instructions](AGENTS.md), the [unchanged source](references/pipethedev-go-deslop.md), and [pinned provenance](references/pipethedev-go-deslop.source.json).

**Three questions currently with the user**

1. Which application will be the first test target? Prefer an application the user controls, with staging access and permission to prepare/reset data. No secrets are needed during this planning discussion.
2. Which Git and CI combination should have the first complete integration: GitHub/Actions, Azure Repos/Pipelines, or GitLab/CI?
3. Who is building the pilot, what is the desired date, and what approximate monthly model/hosting budget is available?

These answers determine the first implementation sequence and deployment assumptions. No answers have been assumed.

**Other decisions to settle before the relevant implementation**

| Decision | Why it matters | Proposed starting position |
|---|---|---|
| Network access | Hosted workers cannot necessarily reach a private staging app | Hosted SaaS with isolated workers if the pilot URL is reachable; add a customer runner immediately only if access requires it |
| Test identities and authentication | Roles, SSO, MFA, invitation and reset flows affect scope | Implement the actual pilot's login method and at least two meaningful permission levels; do not build every auth adapter first |
| Data preparation and cleanup | Exact business checks and repeatability depend on controlled state | Dedicated test organisation, synthetic records, an approved setup/reset mechanism, and a cleanup ledger |
| Agent action boundaries | Exploration may create, delete, invite, or trigger paid side effects | Configured permission to modify designated test resources; use sandbox integrations for external effects |
| Business-rule authority | Source code can contain the bug being tested | Let the agent discover and propose rules; a product owner resolves material ambiguity; freeze required assertions for pipeline runs |
| Architecture boundary | Go backend does not automatically imply Go browser automation | Accepted: Go control plane plus TypeScript/Playwright workers |
| Initial supported frontend stack | Browser testing is broad, but deep source discovery needs stack-aware logic | Optimise source analysis for the pilot stack, and label unsupported analysis limits |
| Test ownership | Existing suites and exported tests change lifecycle design | Inspect/reuse existing tests where useful; retain versioned scenarios; export portability can follow the core proof |
| Data handling | Code/screenshots may leave the customer's environment | Explicit provider choice, synthetic test data, private evidence, short configurable retention; establish any pilot residency restrictions before processing |
| Pipeline enforcement | A noisy early suite can disrupt releases | Run advisory while establishing reliability, then explicitly enable blocking for approved critical scenarios |
| Subscription launch timing | Native-runtime support can expand scope | Validate Sign in with ChatGPT independently; hosted launch depends on partner access |

The accepted defaults above are user decisions. Other starting positions in this table remain proposals, and pilot-specific permissions, authentication, data preparation, and hosting constraints remain unresolved. Missing answers do not prevent further design, but must be resolved before work that depends on them.

**Validation work, rather than questions for the user**

The builder should prove model and browser-worker capability on the chosen app; benchmark meaningful defect detection; verify provider terms for each commercial integration; test isolation and secret handling; measure discovery and replay costs; and validate cancellation, cleanup, deployment identity, and gate outcomes. These are engineering and research tasks, not choices the user needs to make from a menu.

No exact model, hosting service, queue implementation, or package version needs to be locked solely to finish this planning conversation. Choose the smallest justified stack after the pilot access and budget are known. Similarly, branding, payment-provider selection, final pricing tiers, native mobile, and broad enterprise features do not block a technical proof.

**First complete proof to specify**

One pilot application, one Git/CI integration, one managed API provider, the same provider's BYOK path, the pilot's authentication method, two roles, and a small suite of roughly 5–10 meaningful scenarios. The scenario count is a scoping proposal, not a coverage claim.

The proof should connect a repository and deployment, log in with the right identity, prepare known data, propose understandable expectations, execute a dashboard and state-changing journey, catch deliberately wrong behaviour, preserve screenshots/recording and assertions, and return a useful pipeline decision. Repeat against a healthy build and show successful cleanup. Measure false passes, false alarms, run time, model/browser cost, and human clarification effort.

Before accepting the proof, agree which seeded defects it must detect and which valid cases it must pass. Avoid declaring success merely because the agent can navigate or produce convincing reports. A tested specification for this complete path should precede expansion into billing, multiple providers, broad source-language support, or more UI features.

**Next planning output once the three answers arrive**

Create a concise implementation specification with the first user journeys, verified environment assumptions, scenario/rule/run contracts, access boundaries, service/worker API contracts, an ordered backlog, and acceptance checks. Confirm the remaining defaults that materially affect scope, then estimate the first milestone using the actual team and budget. Begin coding only when the user requests it.
