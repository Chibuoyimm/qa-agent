# AI QA tool: research and product plan

Research date: 26 September 2026, Africa/Lagos. Planning only; no application implementation, customer outreach, or vendor trials have been performed.

Current subscription recommendation after policy verification: launch with managed commercial API access and customer API keys. Exclude Google and Anthropic consumer-subscription connections from launch pending an explicitly permitted integration for this product. Earlier subscription UX descriptions are conditional future designs, not approved provider integrations. See section 22.

Confirmed direction: small software teams testing SaaS web applications; start from an existing staging or preview URL; connect the frontend repository and optionally backend repositories for context; use Go for the backend. Model choice, customer credentials, understandable coverage, prompt-driven testing, recorded demonstrations, and automated release-pipeline runs are central to the vision. Release-pipeline integration is part of the initial product scope.

Planning update, 26 September 2026: the user accepted the Go backend / TypeScript-Playwright worker split, bounded agent autonomy, fixed pipeline assertions, advisory-first release checks, a shared managed-AI/BYOK provider adapter, a separate OpenAI subscription prototype, and pilot-stack-focused source analysis. The user also accepted establishing pilot-specific provider consent, hosting-region, and retention requirements; those actual requirements remain to be collected. See [build readiness](build-readiness.md) for the decision record.

Go implementation and review will follow the user-selected [pipethedev Go Codebase Cleanup guide](https://gist.github.com/pipethedev/0bc97d0d4a13edafbad95a00ad8b7ffe). An unchanged pinned copy is retained under `references/`, with project-specific application and repository-handoff instructions in [AGENTS.md](AGENTS.md).

**1. Product thesis**

Build a QA workspace that discovers user journeys, makes their expected behaviour explicit, exercises them in a browser, and gives people understandable evidence of what has and has not been verified.

The valuable promise is: “Know which important customer behaviours were checked, what the checks proved, and what still needs attention.” Treat “thorough” as a systematic, inspectable process, never as a guarantee of complete coverage.

The initial user could be a founder, engineer, product manager, or first QA hire. One technical owner will usually still be needed to provide staging access, test accounts, and a way to prepare data. Once connected, a nontechnical teammate should be able to understand the plan, correct a business rule, add a scenario, run it, and watch the evidence.

Recommended initial niche: data-rich B2B SaaS applications with dashboards, CRUD workflows, roles, and multiple organisations. These provide recurring, valuable correctness questions and a bounded starting domain. This niche is a product hypothesis to validate with prospective customers, not an established market finding.

**2. What the current market already offers**

These are descriptions from first-party materials, not independently verified quality or performance results. A feature absent from this table should not be interpreted as absent from the product.

| Product / approach | Verified positioning or capability | Implication for this product |
|---|---|---|
| QA Wolf | App mapping, prompt-to-test automation, deterministic Playwright/Appium tests, parallel execution, and a managed service | App discovery and readable test coverage already have direct competition |
| Momentic | Agent-assisted authoring and maintenance; its Mo agent tests running apps and reports reproducible bugs; run evidence includes videos and traces | “Give an agent a URL and get bugs” is already an existing offering |
| Meticulous | Records interactions and replays frontend journeys; its described default replays recorded backend responses | Strong regression comparison is a different proof from validating current backend business calculations |
| Playwright Test Agents | Planner, generator, and healer agents with human-readable plans and executable tests | A coding agent plus Playwright is a serious substitute and a necessary benchmark |
| Stagehand | Natural-language actions and extraction alongside deterministic browser APIs | Useful infrastructure to evaluate; it does not supply your business rules or coverage model |

Sources: [QA Wolf](https://www.qawolf.com/), [Momentic documentation](https://momentic.ai/docs), [Meticulous](https://www.meticulous.ai/), [Playwright Test Agents](https://playwright.dev/docs/test-agents), [Stagehand](https://docs.stagehand.dev/v4/first-steps/introduction).

Octomind also surfaced in research as an AI E2E testing competitor, but its main site could not be reliably retrieved in this session. Inspect it during a hands-on comparison. Do not confuse octomind.dev, the testing product, with the separate octomind.run coding-agent product.

The differentiation hypothesis should be the combination of explicit business rules, checks against controlled data, coverage that exposes missing cases, and customer-controlled model access. Neither BYOK nor videos alone is a durable advantage. Validate whether competing products already satisfy the intended workflow before claiming a unique feature.

Potential durable value accumulates in each customer's approved rules, repeatable fixtures, linked journeys, reliable tests, and history of actual defects. This creates continuity across model changes.

**3. The first customer experience**

1. Connect a staging or preview URL and select the repositories that describe that deployment.
2. Provide test accounts and roles, allowed domains, and approved ways of creating test data. Connect a model credential and set a run budget.
3. The tool checks access and produces an environment-readiness report before attempting discovery.
4. The agent explores the live application and reads relevant routes, API clients, schemas, existing tests, and documentation. It produces a draft journey inventory with evidence links.
5. The user reviews a plain-language map: login, onboarding, dashboard, team invitations, billing, reports, and so on. Each area lists scenarios, important expectations, missing access, and open questions.
6. The agent executes cases whose expectations and setup are sufficiently established. It asks focused questions for ambiguous rules and continues independent cases.
7. Results distinguish failed product behaviour, successful checks, uncertain results, and work blocked by missing accounts, data, or environment problems.
8. The user can say “also check whether a removed teammate can still open an old report link.” The request becomes a versioned scenario linked to the relevant permissions rule.
9. Stable scenarios enter the regression suite. Subsequent runs can target a prompt, changed functionality, or the full agreed scope.

Account setup should be progressive. A missing backend repo should reduce the available evidence, not prevent all testing. Explain what additional setup would enable: “Provide a fixture hook to verify the exact revenue calculation.”

Recommended primary screens: project setup; coverage workspace; scenario detail; live run; findings with evidence; model connections and usage. A chat box complements the coverage workspace. It should not be the only place where important rules and unresolved gaps live.

**4. How discovery becomes thorough**

A route is not a journey, and visiting a page is not verifying it. Model a journey as an actor in an initial state performing actions that must produce particular outcomes.

Combine four inputs:

- User intent: product requirements, acceptance criteria, manually supplied rules, and historical bugs.
- Repository evidence: routes, permissions, validations, state changes, API schemas, background jobs, feature flags, and existing tests.
- Runtime evidence: visible controls, accessible names, navigation, network requests, responses, console errors, and screenshots.
- Prepared states: roles, organisations, subscription states, records, and external-service test modes that are actually available.

Use a state-transition model rather than crawling URLs alone. “Invitation sent,” “invitation expired,” and “existing user accepting an invitation” may share a URL but require different scenarios.

For each important journey, systematically consider:

| Dimension | Examples |
|---|---|
| Actor | Guest, member, manager, administrator, revoked user |
| Data state | Empty, one record, many records, archived records, unusual characters |
| Business state | Trial, active, suspended, cancelled; pending, approved, rejected |
| Input | Valid, missing, malformed, duplicate, boundary length or amount |
| Timing | Refresh, back/forward, expired session, delayed response, stale cache |
| Persistence | Reload, new session, second user, subsequent API read |
| Failure | Timeout, server error, partial data, rejected upload |
| Scope | Organisation, owner, date range, currency, feature flag |
| Interaction | Keyboard, narrow viewport, repeated click, interrupted submission |

Do not blindly multiply every dimension. Prioritise by business impact, frequency, recent change, prior defects, and uncertainty. Use equivalence classes and boundary values; use pairwise or higher-order combinations selectively. Combinatorial testing is an established way to reduce combinations, but it does not guarantee every important interaction is covered. [NIST practical combinatorial testing](https://csrc.nist.gov/pubs/sp/800/142/final).

Exploration must have explicit stopping conditions: time, cost, new-state yield, and unresolved prerequisites. Record where it stopped. Deduplicate near-identical scenarios while preserving distinct outcomes. A longer scenario list is not automatically better coverage.

**5. Establishing what is correct**

The hardest problem is the test oracle: the basis for deciding whether behaviour is correct. This is a longstanding testing problem, not something solved merely by giving an LLM the code. [The Oracle Problem in Software Testing](https://philmcminn.com/publications/barr2015.pdf).

Keep these evidence types separate:

| Evidence type | Example | What it establishes |
|---|---|---|
| Structural | Revenue card exists and finishes loading | Basic usability of that component |
| Presentation / consistency | UI displays the amount returned by the API with the right currency | Agreement between presentation and response |
| Independent business expectation | Known paid orders minus known refunds produce the expected total | Correctness for that dataset and approved rule |
| Relationship | Adding an eligible order increases total revenue by its amount | A particular invariant under a controlled change |
| Baseline comparison | The current screen differs from an approved screenshot | Change from a baseline; the baseline itself might be wrong |
| Semantic judgement | A chart looks clipped or an error message is misleading | A potentially useful finding requiring calibrated judgement |

For high-impact business claims, prefer approved rules plus independent, controlled expected values. Do not copy a calculation from the implementation and present the resulting agreement as independent verification. Do not call the same aggregate endpoint for both actual and expected values.

Code can suggest expected behaviour, but store such a rule as inferred until corroborated. Requirements can be outdated too. If code, requirements, and observed behaviour disagree, expose the conflict and its sources.

Use ordinary code for numeric comparisons, sets, permissions, dates, persistence, and invariant calculations. Use LLMs to propose cases, interpret ambiguous content, and investigate failures. A screenshot model can suggest that a chart is wrong; it should not be the sole authority for financial arithmetic.

Attach to every important assertion: rule ID and version, provenance, whether it is approved or inferred, data requirements, actual observation, expected result, comparison method, tolerance if relevant, and evidence IDs. “Confidence” should describe evidence strength, not an invented probability of correctness.

An empty dashboard can legitimately display zero. The tool must distinguish “zero is valid for this fixture” from “the API failed and the UI silently displayed zero.” See the companion dashboard specification for the full worked example.

**6. Repository access and environments**

There are three independent axes: what code is available, where the tested app runs, and where the browser runs. Avoid conflating them.

| Setup | What it enables | Important limit |
|---|---|---|
| URL only | Browser exploration and observable behaviour | Hidden features and intended rules remain uncertain |
| URL + frontend repo | Routes, client validations, requests, feature flags, component context | Backend implementation is unavailable |
| URL + frontend and backend repos | Better understanding of calculations, permissions, jobs, and fixture opportunities | Code access still does not prove which revision is deployed |
| Customer runner + URL | Access to private networks and customer-managed secrets | Runtime and outbound model-data policy still need configuration |
| Tool-hosted full app | Potentially reproducible infrastructure and data | Arbitrary application provisioning is a substantial separate product |

Frontend-only repository access can still test a real backend through the deployed app. Conversely, both repos plus mocked backend responses do not constitute full-system verification.

Recommended launch: existing URL, read-only repository context, and hosted Chromium workers. Design the worker contract to support customer runners later. Add a private runner sooner only if access is blocking pilot customers.

Store a deployment manifest for each run: URL, environment identity, frontend SHA, backend SHA(s), deployment ID, relevant flags, browser version, test revision, fixture version, and model configuration. If a SHA is unknown, say so; reading the latest branch is not proof of the deployed version. Recheck deployment identity at the end and flag runs that straddle a deployment.

Treat repository sync as versioned indexing, not blindly cloning everything for every run. Start with file inventory, text search, parsers where valuable, and targeted retrieval. A vector database is optional, not an initial dependency. Use API schemas to connect frontend actions to backend concepts; capture dynamic links during exploration, since static call graphs will be incomplete.

For GitHub, prefer a selected-repository GitHub App with minimal permissions. Keep read-only context access separate from optional future test-export PR permissions. GitHub installation tokens support restricted repository and permission scopes. [GitHub App access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app).

**7. Data setup is part of the product**

Offer a progressive fixture contract: `prepare`, `verify_ready`, and `cleanup`, associated with a run and scenario. Implementation can be a customer-owned staging API, an approved setup script, or browser setup. Prefer stable application APIs for preparing unrelated data and use the browser for the behaviour under test.

Preparation must declare expected state and return identifiers and receipts. Verify preparation succeeded before testing. Scope resources by run ID; use separate organisations or accounts for parallel tests where possible. A fresh browser context does not isolate shared server data.

Use synthetic data and sandbox payment/email services. Support a test inbox when invitation or password-reset flows require it. For SSO and MFA, support a documented test identity or a human-assisted session; lack of a usable authentication path is a visible blocker.

Model eventual consistency explicitly. A background job may legitimately update a total later; assert within an agreed deadline and poll the relevant state. Avoid arbitrary sleeps and do not hide a slow product behind repeatedly increased timeouts.

Cleanup runs after success, failure, cancellation, and worker crashes through a separate reconciliation mechanism. Persist a resource ledger and show cleanup failures. Do not claim exact numerical correctness on uncontrolled shared staging data unless the expectation can be independently established for that state.

Network fault injection can test how the frontend handles a failed response. Mark the test as fault-injected. It does not prove the real backend produced or recovered from that failure.

**8. Coverage and results that people can trust**

Separate planning state, execution result, and evidence strength; one badge should not carry all three meanings.

- Planning: discovered, proposed, accepted, implemented, retired.
- Execution: not run, running, passed, failed, blocked, inconclusive, cancelled.
- Freshness and reliability: current, stale, unstable; preserve attempt history.
- Evidence: UI observation, API consistency, independent fixture, approved rule, inferred rule, visual judgement.

Plain-language example: “Dashboard: 8 of 12 agreed scenarios checked on this deployment. Seven passed; one failed. Two need a manager account; two have not run. Revenue display matches the API, but refund handling is not independently verified.”

Always define the denominator. Count accepted scenarios in a named, versioned scope, plus separately listed discoveries awaiting review and acknowledged unknown areas. Keep blocked and not-run cases in the denominator. Do not shrink it silently to make a score improve.

If a summary percentage is necessary, separate:

- Execution coverage: completed evaluable cases / all agreed in-scope cases. Failures count as executed.
- Pass rate: passing cases / completed evaluable cases.
- Business-rule verification: current independent checks / agreed rules requiring them.
- Optional risk-weighted coverage using visible, agreed weights.

These are conditional on the known scope. None is “percentage of the whole app proven correct.” Feature flags, inaccessible roles, unknown requirements, and unobserved states remain explicit discovery limitations.

Prioritise gaps as actions: “Add a second organisation to check isolation” is more helpful than “low confidence.” Make ordinary user corrections durable: one correction to revenue semantics should update all affected scenarios and invalidate stale results.

**9. AI exploration and repeatable execution**

Use two execution modes with shared scenario and evidence models.

Exploration mode lets an agent inspect context, choose actions, discover states, and propose or investigate cases. Bound its tools, cost, actions, time, and retries. It can use DOM/accessibility observations with screenshots when visual information matters.

Regression mode executes frozen scenario versions and deterministic assertions. Models should not need to rediscover every click on every run. If a test requires an LLM judgement, identify that explicitly and measure its stability separately.

A lifecycle is: discover → propose scenario and oracle → resolve material unknowns → prepare data → execute → capture evidence → classify → reproduce where appropriate → promote to regression.

Keep the original failure when repairing a test. Locator or navigation repairs can be suggested, but changing expected totals, permission rules, fixture semantics, or assertions requires a visible semantic change. Never turn a failing test into a passing result by deleting its assertion, changing its expected value to the current UI, or silently skipping it.

After a repair, create a new test version and rerun. Preserve the original attempt and reason. Passing on retry is evidence of instability, not permission to erase the initial failure.

Playwright's existing agents are useful reference machinery. The product must enforce its own assertion and promotion policies around any borrowed planner or healer. [Playwright agent workflow](https://playwright.dev/docs/test-agents).

**10. Go backend and browser architecture**

Recommended starting shape: a Go modular monolith for the control plane, PostgreSQL, a durable Go job queue, object storage, and isolated browser workers running TypeScript/Playwright Test. One codebase can provide the API and background Go worker as separate processes.

Go owns tenancy, projects, repository connections, scenario versions, rules, model routing, budgets, scheduling, run state, evidence metadata, reporting, and access policy. TypeScript owns the browser/test lifecycle and exports structured events and artifacts. This is compatible with a Go backend; it does not require rebuilding the browser testing ecosystem in Go.

Playwright officially documents JavaScript/TypeScript, Python, Java, and .NET; the Node test runner includes useful testing integrations. Prefer it to introducing a Go binding solely for language uniformity. [Playwright supported languages](https://playwright.dev/docs/languages). Stagehand currently also documents Go examples; evaluate it for exploration if useful rather than assuming all AI browser tooling requires TypeScript.

Start with a small, versioned worker protocol: run ID, attempt ID, scenario revision, capability requirements, authorised environment, fixture references, time budget, and artifact destination. Return ordered events, actual observations, assertion results, and artifact manifests. Use HTTP/JSON initially; adopt gRPC only for a demonstrated need. Never place raw secrets in queue payloads or event streams.

Two queue choices are credible:

| Choice | Suitable use | Trade-off |
|---|---|---|
| River + explicit PostgreSQL state machine | Short bounded runs and a simple initial deployment | You own stage transitions, reconciliation, and longer approval flows |
| Temporal Go SDK | Durable multi-stage workflows, long waits, cancellation, and complex recovery | Extra infrastructure and workflow programming constraints |

Recommendation: River for the first bounded staging-URL prototype; choose Temporal before production if durable waiting and multi-stage recovery become central. Do not run both initially. River provides a Go/PostgreSQL queue; some advanced features are paid, so assess the exact required edition. [River](https://riverqueue.com/docs), [Temporal Go](https://docs.temporal.io/develop/go).

Whichever engine is chosen, external actions and model calls are nondeterministic. Assume delivery and activity retries can repeat work. Give attempts fencing tokens; use idempotent setup and uploads; check outcomes before retrying ambiguous destructive browser actions. A workflow engine cannot make a click or payment exactly once.

Persist transitions transactionally with queued follow-up work. Use leases/heartbeats, cancellation, per-tenant concurrency, and a reconciler for stuck runs and orphaned resources. Keep orchestration retry policy separate from test retries and agent recovery attempts. Repeated infrastructure problems must not multiply browser actions without limit.

Logical data model: organisation, member, project, repository snapshot, deployment, environment, credential reference, role/account, business rule and revision, journey, scenario and revision, test artifact, fixture recipe, run, attempt, assertion result, finding, artifact, usage event, and audit event. Tenant identity must constrain every lookup and artifact URL. JSONB is useful for evolving scenario definitions; relational columns should hold identities, state, ownership, and query-critical fields.

Do not start with a graph database, vector database, many microservices, or Kubernetes solely because the product uses agents. A graph-shaped product model can live in PostgreSQL. Worker isolation is a security requirement; a sprawling service topology is not.

**11. Bringing API keys and subscriptions**

Separate two integration types in the domain model and eventually in the UI:

- Model provider: your QA harness controls the loop and calls a provider API with customer credentials.
- Agent runtime: a provider's agent runs its own loop through an official CLI/SDK; your product supplies bounded tasks, tools, and evidence requirements.

Switching between these is more than changing a model string. The agent's prompts, tool semantics, context management, billing, and permission handling may change. Benchmark each supported runtime as a separate configuration.

| Platform | Direct API path | What current official documentation establishes about subscriptions |
|---|---|---|
| OpenAI | Customer Platform API key | Codex supports ChatGPT subscription sign-in; API access has separate billing. Current docs also describe Enterprise Codex access tokens for trusted automation. These are Codex-specific capabilities, not a universal ChatGPT-backed API for your SaaS |
| Anthropic | Customer Claude API key or supported cloud-provider credentials | Current legal documentation permits end users signing into the unmodified Claude Code binary under specified hosting conditions, while restricting third-party collection/intermediation of subscription credentials. Other SDK and help pages do not fully align; validate the exact integration before offering it |
| Google | Gemini API key, or later Vertex AI credentials | Official policy prohibits third-party direct access to services behind Gemini CLI using CLI OAuth, and warns of suspension/termination. Antigravity has a corresponding restriction. Exclude a consumer-subscription connection from launch; do not infer permission from CLI sign-in support |

OpenAI source: [Codex authentication](https://learn.chatgpt.com/docs/auth). Google sources: [Gemini CLI terms notice](https://geminicli.com/docs/resources/tos-privacy/), [Antigravity FAQ](https://www.antigravity.google/docs/faq/), and [Gemini API billing](https://ai.google.dev/gemini-api/docs/billing).

Anthropic requires particular care in interpreting current pages. Its [legal and compliance page](https://code.claude.com/docs/en/legal-and-compliance) distinguishes hosting an unmodified binary with user-owned authentication from implementing your own subscription login or routing layer. Its [SDK overview](https://code.claude.com/docs/en/agent-sdk/overview) still directs third-party developers toward API authentication absent prior approval. A [Help Center update dated 16 June 2026](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan) says a proposed SDK billing change was paused and eligible SDK/third-party usage continues drawing subscription limits. These pages should not be collapsed into either “all subscription use is forbidden” or “any wrapper is allowed.” Treat the precise commercial/runtime design as unresolved pending provider clarification.

Recommendation: establish OpenAI, Anthropic, and Gemini API adapters as the baseline. Implement one end-to-end during the proof of concept, a second before committing the abstraction, and the third for the intended multi-provider launch. Benchmark them rather than selecting a permanent winner from general coding leaderboards.

Investigate native runtime integrations as a separate feasibility track. A customer-run companion may help keep provider sign-in local, but locality alone does not establish permission. Do not collect consumer passwords, copy session cookies into your service, imitate official clients, or share individual subscription pools across organisations.

A useful adapter interface normalises text, image inputs, structured tool calls, tool results, cancellation, provider errors, usage, and model metadata while allowing capability differences. Track structured output, vision, context limits, rate limits, authentication type, region, retention options, and model-version support. Avoid forcing every provider into an OpenAI-compatible endpoint shape.

Keep available model selection tied to the customer's actual credentials and a tested capability registry. A model that cannot inspect images may still plan cases, but should not be selected for a visual inspection task without another supported path. Record exact model IDs and revisions where offered; an alias can change and reduce reproducibility.

Use the stronger reasoning model for discovery and difficult diagnosis; prefer deterministic code for replay. Smaller models can summarise evidence if their factual fidelity is evaluated. Never silently route customer code to an unapproved provider when a quota is exhausted. Offer an explicit choice to pause, change model, or use a preapproved fallback.

BYOK reduces your inference bill but not browser infrastructure, storage, support, or orchestration costs. Users need to see platform charges and estimated provider charges separately. Do not promise an exact remaining subscription quota unless an official interface supplies it.

**12. Security boundaries that follow from this design**

This product handles private code, app credentials, browser sessions, customer data, and potentially executable test code. Build the relevant boundaries into the first pilot rather than treating them as a later enterprise feature.

- Keep the control plane outside execution sandboxes. Generated tests and repository content must not access its database, cloud credentials, or other tenants.
- Use disposable workers with process, filesystem, resource, and network isolation. Evaluate a hardened sandbox or VM boundary for untrusted executable code; an ordinary container by itself is not a complete isolation argument.
- Use a broker for model credentials so browser/test processes cannot read raw provider keys. Scope fixture and application credentials to the smallest required environment and lifetime.
- Enforce authorised network destinations outside the model, including redirects, DNS resolution, private address ranges, and cloud metadata access. A private runner needs explicit permitted private targets, not unrestricted internal access.
- Treat page text, repository comments, documents, and screenshots as untrusted observations. They cannot amend tool permissions, reveal secrets, or change accepted assertions.
- Staging credentials should be unable to perform real production actions. Provide explicit action policy for payments, messages, invitations, exports, and destructive operations; synthetic accounts and sandbox integrations reduce the need for repeated interruptions.
- Do not execute repository setup hooks or agent instructions automatically merely because the repository contains them. Code-context mode can remain read-only.
- Protect artifact viewing with tenant-scoped authorisation, short-lived links, retention controls, and audit logs. Redact network headers, secrets, and sensitive fields before model submission and evidence storage where possible.
- Treat video, screenshots, DOM snapshots, traces, and downloads as sensitive. Redaction of one surface does not redact the others. Prefer synthetic data and restricted capture; do not promise perfect automatic redaction.

These are proposed engineering controls, not a claim of compliance certification. Playwright MCP explicitly notes that it is not a security boundary; its origin controls are not a replacement for isolation and network enforcement. [Playwright MCP security guidance](https://github.com/microsoft/playwright-mcp).

Privacy mode must explain what leaves the customer's environment. A local browser still sends code, screenshots, or observations to an external model if configured that way. Support provider allowlists and retention settings; validate provider-specific data policies before commercial onboarding.

**13. Evidence, findings, and short videos**

Each finding should answer: which user was affected, what the agent did, what should have happened and why, what actually happened, how it was reproduced, and which evidence supports the conclusion. Separate observed failure from suspected root cause. A matching backend file is a lead, not proof that it caused the runtime result.

Capture a structured action/assertion timeline, screenshots at meaningful states, network and console evidence as needed, a trace, and optional browser video. Playwright already supports video recording and rich traces, so an initial evidence viewer can reuse those capabilities. [Playwright videos](https://playwright.dev/docs/videos), [Trace Viewer](https://playwright.dev/docs/trace-viewer).

The “small demo video” can have two useful meanings:

- Failure clip: the shortest useful recording that shows the setup, action, and failure.
- Verified walkthrough: a successful journey presented as a short demonstration with captions and a date/deployment label.

For the first version, use real run recordings with text captions derived from logged events. Later, trim pauses and add optional narration. Preserve the unedited recording and timestamp mapping. A recording proves what was shown; the linked assertions establish what was checked.

Record the initial attempt where feasible. Recording only a retry can lose evidence of an intermittent issue. Close the browser context so recordings finish writing, upload artifacts, then finalise the manifest. Mark interrupted or missing recordings honestly rather than making them a reason to report a passed test.

Never generate a synthetic video and present it as a recording of a test. Do not quietly recreate a failure clip on a different deployment. Prefer a 20–60 second useful clip as a design target, not a hard requirement that hides needed setup.

**14. Evaluating the QA tool itself**

Browser task completion and bug detection are different capabilities. Recent research reinforces this distinction: WebTestBench evaluates the web-testing process, while CATTest evaluates bug discovery in generated web applications. Their findings motivate dedicated QA evaluation, but their datasets are not a forecast of performance on this product's target customers. [WebTestBench](https://arxiv.org/abs/2603.25226), [CATTest](https://arxiv.org/abs/2609.00081).

Build a controlled evaluation corpus before relying on persuasive demos. Include a few representative SaaS apps, known-good versions, historical defects, and deliberately seeded defects. Use both frontend-only and frontend-plus-backend context configurations. Hold back some apps and defect variants from prompt development.

The corpus should include defects that leave the page apparently healthy: wrong totals, incorrect date boundaries, cross-organisation leakage, a missing permission check, duplicate creation on retry, stale results after a mutation, a success toast without persistence, a broken export, and an error represented as a legitimate zero.

Separate evaluation stages:

1. Discovery: with no list of injected defects, can the agent identify relevant journeys and meaningful expectations?
2. Oracle quality: are its expectations supported, and can an independent reviewer distinguish facts from assumptions?
3. Execution: can it perform accepted scenarios on a healthy build without spurious failures?
4. Detection: do frozen tests detect relevant seeded defects, including defects present before initial discovery?
5. Maintenance: can it survive harmless UI changes while continuing to catch semantic regressions?
6. Reporting: does each bug report contain valid, reproducible evidence without unsupported causal claims?

Baseline comparisons should include a coding agent with Playwright's planner/generator, an existing hand-authored suite where available, and a time-bounded human exploratory pass. Use comparable environment access, task scope, time, and cost budgets. Human QA is a useful comparator, not an exhaustive oracle.

Record these metrics with sample counts and uncertainty:

| Metric | Definition / decision it informs |
|---|---|
| Defect recall | Detected relevant seeded defects / relevant seeded defects; report by severity and class |
| Finding precision | Independently confirmed product defects / findings labelled product defects |
| False-pass rate | Known-bad scenario executions reported as successful / known-bad executions |
| Clean-build stability | Repeat results on unchanged known-good app, test, and fixture versions |
| Oracle validity | Sampled assertions with defensible expectations / sampled assertions |
| Human effort | Onboarding, rule clarification, triage, and maintenance minutes |
| Coverage usefulness | Important missing scenarios found by humans after reviewing the generated plan |
| Cost and speed | Provider usage, browser minutes, latency distribution, and cost per confirmed useful finding |
| Cleanup reliability | Run-owned resources successfully removed or explicitly reconciled |
| Repair integrity | Repairs that preserve approved semantics and retain detection capability |

Provisional pilot targets, to calibrate rather than advertise: detect at least 90% of the curated high-severity fixture-backed defects; at least 95% precision on findings labelled confirmed; at least 98% stable executions on repeated healthy-build cases; and no silent assertion weakening. A small sample can produce flattering percentages, so always show counts, severity, and confidence intervals where meaningful.

Use the benchmark to compare providers and harness revisions. Promote a model or prompt change only after checking quality, cost, stability, and regression detection. A faster model that misses permissions defects can be the wrong optimisation.

**15. Scope and phased delivery**

The phases below are an order of validation. Effort ranges are rough planning estimates for one experienced full-time builder with usable pilot environments; they are not delivery commitments and should be revised after the proof of concept.

| Phase | Deliverable | Exit condition | Rough effort |
|---|---|---|---|
| Product validation | Interviews, workflow review, sample reports, competitor walkthroughs | A small set of teams will provide staging access and judge results; concrete willingness-to-pay signal | 1–2 weeks |
| Technical proof | One supported SaaS app, repo context, bounded exploration, fixture-backed assertions, recordings, one CI-triggered run | It detects meaningful seeded defects, reruns healthy cases consistently, and blocks a deliberately broken release candidate | 2–4 weeks |
| Private pilot | Go control plane, coverage workspace, durable runs, two API providers, repeatable fixtures, artifact access, CLI/API release gate | Two or three teams can operate it across multiple releases with measured support effort and reliable pipeline outcomes | 4–6 weeks |
| Launch candidate | Third initial API provider, richer deployment integrations, budget controls, security hardening, export, billing | Acceptable quality and economics, reliable cleanup, and scoped support commitments | 4–8 weeks |

That suggests roughly 11–20 builder-weeks to a narrow launch candidate, with substantial uncertainty. Complicated authentication, unsafe test environments, or poor defect precision can dominate the schedule. Replan at each gate rather than filling a calendar with unproven work.

Initial scope: Chromium web apps, desktop plus a selected narrow viewport, one Git provider, staging URL, frontend repo with optional backend context, supported test-account authentication, journey discovery, visible business rules, customer API keys, scenario editing by prompt, manual and CI-triggered runs, a CLI/API release gate, frozen regression tests, and evidence clips.

Add next: richer native PR/deployment integrations, scheduled suites, more roles, more fixture adapters, customer runners, Firefox/WebKit on selected journeys, native provider runtimes if their feasibility track succeeds, and exportable Playwright suites.

Defer: arbitrary full-stack hosting, native mobile, load testing, comprehensive security auditing, complete accessibility certification, automatic production testing, a universal visual editor, and fixing application code. Role/tenant isolation checks and basic accessibility checks can still belong to functional QA without claiming a full security or accessibility audit.

Test export should include assertions, fixture requirements, and dependency versions. If a case relies on hosted semantic evaluation or proprietary runtime features, disclose that instead of claiming it is fully standalone.

**16. Economics and operating limits**

Instrument costs before deciding a price. For each run, estimate:

`provider input + provider output + image/tool charges + browser time + fixture/setup time + artifact storage/egress + expected support/triage`

Track discovery, regression, and investigation separately. Discovery can be relatively expensive but reusable; a stable replay should usually have little or no model inference. Do not run a full rediscovery on every deployment.

Illustrative arithmetic only: a 40-scenario suite averaging 75 browser-seconds consumes 50 browser-minutes before retries and setup. Five concurrent workers could approach 10 minutes of browser execution under ideal scheduling, but concurrency does not reduce the underlying 50 browser-minutes of work.

A revised commercial hypothesis is a workspace/project subscription with included browser usage, storage, and a bounded managed-AI allowance. Recommend managed AI as the onboarding default, while retaining customer API keys and supported subscription runtimes as alternatives. Validate the allowance and price against pilot costs and customer preferences; BYOK customers still pay for the platform and execution resources.

Avoid pricing by number of generated tests; it encourages noisy, redundant plans. Investigate value in release confidence and saved QA time, with metered expensive resources. BYOK should not be marketed as making the service free to run.

Set limits on tokens, tool calls, elapsed time, browser actions, pages/states explored, concurrency, and retries. Reserve estimated budget before calls and reconcile usage afterward. Explain that an in-flight provider request can still incur charges after cancellation and estimates are not the provider's final invoice. Some providers also report billing with delays; Google documents this explicitly. [Gemini billing processing](https://ai.google.dev/gemini-api/docs/billing).

**17. Discovery work before implementation**

Interview approximately 8–12 target teams and seek 2–3 design partners. These are recommended next research activities; nobody has been contacted. Ask for examples of the last escaped bug, current manual regression checklist, existing staging data/reset approach, roles and SSO constraints, release frequency, and how much time they spend distinguishing real failures from flaky tests.

Show the worked dashboard scenario and a coverage report. Ask participants to identify what they believe was proved and what remains unchecked. If they misread the evidence, revise the report before investing in more agent autonomy.

During competitor trials, use the same small application and scenarios. Examine whether each tool handles an already-wrong aggregate, exposes the basis for the expected result, preserves failure history after repair, supports the needed credentials, and produces a report a product manager can interpret. Public documentation alone cannot resolve those questions.

Validate these unresolved decisions in order:

1. Which target teams can provide a repeatable fixture or isolated test organisation?
2. Which authentication patterns must the first pilot support?
3. Will repo access and external model processing be acceptable?
4. Is exact business verification valuable enough to justify setup effort?
5. How much human clarification is tolerable before first useful results?
6. Does BYOK win customers, or mostly add onboarding friction?
7. Is a customer runner necessary for the initial segment?
8. Which native subscription runtime integrations are actually supported for the intended product design?

The strongest first technical demonstration is a dashboard and a state-changing workflow tested against known data: discover the journeys, explain the expectations, catch an intentionally wrong result on a page that still loads, reproduce it, and show the evidence. That demonstration tests the central product thesis much better than a large number of successful clicks.

**18. Automated release-pipeline execution**

User addition: the product must be usable automatically as a release-pipeline step. Treat this as a core use case. Interactive prompts, pipeline triggers, and later schedules should create runs through the same execution service and produce the same evidence model. This section describes the proposed product; no pipeline or scheduled automation has been created.

Recommended release sequence:

1. The customer's pipeline builds the candidate artifacts and performs its existing unit/integration checks.
2. It deploys those artifacts to an isolated preview or controlled staging environment.
3. The QA step submits the target URL, deployment identity, frontend/backend revisions or artifact digests, suite revision, gate-policy revision, and budget.
4. The service verifies readiness and the deployed identity, prepares isolated data, and executes the required scenarios.
5. It finalises assertions and essential evidence, then returns a structured gate decision with a report URL and machine-readable results.
6. The pipeline promotes the tested artifacts only if its configured gate succeeds. A failed or incomplete required check prevents promotion under the recommended default policy.

Testing a commit on staging and then rebuilding different artifacts for production weakens the connection between test and release. Preserve artifact identity where possible; record intentional environment differences. A passing staging result does not prove untested production configuration is correct.

Support both a simple synchronous CLI experience that submits and waits, and asynchronous API submission with polling or authenticated callbacks. The hosted service can execute tests while the CLI waits; customers should not need to install browsers in their CI job for this mode. A customer-run browser worker is a later execution option for private network access.

The CLI should export a structured JSON result, JUnit XML where useful, a readable job summary, and the authenticated report URL. Reserve success exit status for a satisfied gate; distinguish product failure from incomplete execution in the structured result and preferably in exit codes. In advisory mode the CLI may allow pipeline continuation, but must retain the real test result and label the run non-gating. All interfaces are proposed, not existing commands.

Keep this integration CI-provider independent. A thin adapter can work with GitHub Actions, GitLab CI, Azure Pipelines, or other systems that invoke a command or API. Build one reference integration initially. Playwright supports CI execution across providers; GitHub Actions also supplies job dependency and deployment controls that can enforce the customer's promotion policy. [Playwright CI](https://playwright.dev/docs/ci-intro), [GitHub deployment workflows](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/control-deployments).

Use different scopes for different triggers:

| Trigger | Suggested work | Default product posture |
|---|---|---|
| PR preview ready | Critical smoke cases plus relevant established scenarios | Gate the agreed checks; surface new gaps separately |
| Release candidate ready | Full required suite for that release, with known roles/data states | Release gate |
| Nightly or scheduled run | Broader regression and bounded exploration for missing cases | Findings and coverage proposals; independently configured gating |
| Manual prompt | Targeted investigation or new scenario discovery | Save results and offer promotion into regression |

Impact-based selection is an optimisation with imperfect dependency information. Always run a mandatory critical baseline, show omitted scenarios, and use the broader suite when impact mapping is uncertain. Both frontend-only and backend-only changes can trigger relevant browser journeys. Multi-repository releases must bind the result to the actual deployed combination, not whichever branch tips were most recently indexed.

A release gate needs a versioned decision policy independent of model-generated prose. Recommended default: all required scenarios must execute successfully on the intended candidate with adequate evidence. Product failures block. Missing credentials, exhausted model quota for a required check, environment failure, timeout, zero selected tests, cancellation, and deployment mismatch produce an incomplete or invalid gate and also prevent promotion. Optional checks may warn. A retry pass remains visibly unstable and follows an explicit flake policy rather than automatically erasing failure.

Freeze the required scope, rules, and assertions at run creation. The agent can investigate failures and propose repairs in parallel, but cannot weaken the gate, remove scenarios, or change expectations during the same run. An accepted repair creates a new suite revision and a new evaluated run. An operator override records who bypassed the gate, why, and for which deployment; it does not rewrite failed tests as passed.

Unattended execution cannot depend on answering chat questions. Resolve essential roles, rules, and fixtures before adding a case to the required release suite. During a pipeline run, unresolved prerequisites become explicit blockers while independent cases continue. Bounded exploratory checks can run alongside the suite, but any new finding should affect the gate only under an explicit evidence and severity policy.

Use project-scoped service credentials, preferably short-lived CI identity where supported, or a revocable service token initially. The pipeline only needs permission to trigger/read runs for its project; it should not receive customer model keys or arbitrary access to other environments. Treat untrusted PR code and fork events separately from privileged staging credentials.

Operational requirements: idempotent submission keyed by pipeline execution and attempt; explicit new attempts for deliberate reruns; authenticated and deduplicated callbacks; bounded queues and execution deadlines; cancellation propagation; cleanup even after the CI process disconnects; safe reconnect to an existing run; and no reuse of a pass for another deployment. If staging is shared, enforce an environment lease or invalidate results when the deployed candidate changes.

Regression runs with fully deterministic assertions should not require a live model call merely to execute existing tests. Model-provider outages may delay discovery or diagnosis without preventing such cases from running. Cases that require semantic model judgements must declare that dependency and cannot pass when the judgement is unavailable. Reserve execution capacity for release runs so nightly exploration cannot consume every worker or the entire model budget.

First pipeline acceptance exercise: a healthy candidate passes; an injected incorrect dashboard total blocks promotion and links its recording; missing test credentials yield an incomplete gate; duplicate submission returns the same run; cancellation cleans up; and a deployment change during testing invalidates the result. Compare against the same run launched manually to verify consistent rules and outcomes.

**19. Application credentials, identities, and authenticated testing**

Make application access a first-class project capability. These credentials belong to the customer's application under test, distinct from AI-provider keys, repository tokens, and the service credential that triggers a pipeline run. Model each separately so access, rotation, and audit policies cannot be accidentally conflated.

The customer configures named test identities for an environment: for example, Staging Admin, Staging Member, Read-only User, and Member in Other Organisation. An identity records the login method, secret references, expected account and organisation, expected role/permissions, permitted origins, authentication recipe revision, and whether unattended authentication is supported. Roles are customer-defined; the tool should not assume every application has the same permission model.

Project setup should provide an application-access screen that lets an authorised person add accounts, choose the login method, test the connection, view authentication health, and replace or revoke credentials. Show status such as Ready for pipeline runs, Requires human sign-in, or Connection failed. Verify the actual authenticated identity and organisation after login using a reliable application-specific signal; reaching any dashboard is insufficient. Ask for no passwords in chat, generated test files, or source control.

Recommended authentication paths:

| Method | Proposed handling | Unattended suitability |
|---|---|---|
| Email/username and password | Secure credential entry or external secret reference; a trusted browser action fills the configured login form | Suitable when no interactive challenge is required |
| Email OTP or magic link | Dedicated test mailbox integration; retrieve the message tied to the run, account, and request time | Suitable with a supported, reliable mailbox path |
| TOTP MFA | Customer-authorised test identity with securely held test authenticator seed; generate codes in a trusted component | Suitable for an explicitly supported test setup |
| SSO | Dedicated test identity/tenant and supported identity-provider flow; allow all required origins explicitly | Depends on the organisation's sign-in and device policies |
| Interactive approval, device-bound passkey, or CAPTCHA | Human-assisted connection for exploratory runs, or a customer-configured automation-compatible test environment | Do not promise unattended renewal without a supported path |
| Application API/session setup | Customer-provided authorised authentication adapter for non-login scenarios | Suitable when session creation and renewal are supported |

Do not weaken production authentication to make tests run. When an owner configures a test-only authentication mechanism or provider test mode, record the difference and do not count it as verification of the real production login mechanism. Bound OTP polling and password retries to avoid consuming unrelated messages or locking accounts. Never guess replacement credentials or silently escalate to an administrator account.

Separate two purposes: testing login itself, and establishing a session for another journey. Login/logout/MFA/reset/expiry/revocation scenarios must exercise their intended authentication behaviour from the correct starting state. Dashboard or report tests can reuse a prepared session where safe. Reusing a session does not prove that the login page works. Playwright supports saved authenticated state, API-based authentication, and separate accounts for parallel workers that modify state. Saved state is sensitive and requires protection. [Playwright authentication](https://playwright.dev/docs/auth).

Authentication recipes should be discovered or recorded once, verified, then versioned. The model can request a named identity and reference a secret by opaque handle; a trusted execution component resolves it just in time and checks the target origin before filling it. Never include raw secret values in model prompts or ordinary tool results. This reduces exposure but is not a claim that hostile pages or arbitrary code in an authenticated browser cannot access session data; worker isolation, restricted egress, narrow permissions, and synthetic test accounts remain necessary.

Use a managed secret store or envelope encryption backed by a managed key service, with per-project/environment access controls, audited use, rotation, and deletion. The product must retrieve application passwords to log in, so one-way password hashing alone cannot implement this credential store. Keep plaintext lifetime short and exclude values from queue payloads and telemetry. Later, support customer secret-manager references and runner-local resolution so the hosted control plane stores only references.

Treat cookies, refresh tokens, local storage, and other reusable authentication state as credentials. Encrypt any persisted state, limit its lifetime, and restrict it to the correct project, environment, identity, browser/runtime, and permitted origins. Reuse within a run by default; longer caching is an explicit policy. Invalidate cached sessions on relevant secret or authentication-configuration changes. Deleting a stored session does not necessarily revoke the application's server-side session; use the application's revocation mechanism when available and report its limits.

Exclude or protect authentication capture across screenshots, video, DOM snapshots, traces, request bodies, headers, and downloads. Password masking in the page does not protect the other channels. Disable sensitive capture during setup where necessary; authentication tests can retain a restricted or sanitised evidence record. Never attach a saved browser session to a normal bug report.

Parallel execution needs identity leases or an account pool. Tests that change passwords, log out all sessions, revoke access, or mutate shared settings need disposable accounts or explicit exclusive use. A new browser context alone cannot prevent these server-side collisions. Verify and reset state before returning an account to the pool; quarantine accounts whose cleanup or reset failed.

Before a pipeline run, preflight required identities and test whether their configured authentication path is available. If a cached session is invalid before a scenario starts, renew it within a bounded policy. During a scenario, do not silently reauthenticate if doing so would mask the expired-session, logout, or revocation behaviour being tested. Preserve the observed event and classify it using the scenario's expectations.

Distinguish authentication failure causes in reports. A missing secret or expired human-assisted session is a setup blocker. A valid test account rejected by a broken login flow may be a product defect, especially in a login scenario; do not automatically label every login failure as infrastructure. Dependent scenarios may remain blocked while the login failure is reported separately. A required gate never passes solely because authenticated tests could not run.

Initial scope: secure application credential storage, named role/organisation identities, versioned password-login recipes, identity verification, bounded session reuse, credential-health checks, account isolation for parallel mutations, and explicit pipeline blockers. Add dedicated test email support early when pilot journeys need invitations or resets. Schedule TOTP, SSO, external secret-manager integrations, and specialised authentication adapters according to pilot requirements; unsupported interactive methods must be visible during onboarding.

Acceptance checks for this subsystem: correct role/organisation verification; no reuse across tenants or environments; rotation invalidates the relevant cache; revoked access remains revoked during its test; concurrent identity-changing scenarios cannot share an account; OTP retrieval does not consume another run's message; secret values do not appear in normal logs or reports; and a disconnected CI client does not leave identity leases permanently held.

**20. AI connection setup: API keys and subscriptions**

Provide a Settings → AI connections area. Each connection has a name, owner, provider, integration type, authorised project scope, execution location, available capabilities/models, credential reference, health, and usage policy. Keep these connections separate from the application credentials described above. This is a proposed design; no provider account has been connected.

The user chooses Add connection → provider → supported connection method. Offer API key as the broadly supported baseline. Show a subscription option only for a provider/runtime integration that has been validated for the product's exact use case; do not render every provider with an apparently interchangeable subscription button.

API-key setup:

1. Select OpenAI, Anthropic, or Google and name the connection, such as Company release tests.
2. Enter a key through the secure form or choose an external-secret reference when that feature is available. Explain that provider API usage is billed separately from our platform fee.
3. Validate authentication server-side. Where model-list access is unavailable, use a maintained capability catalog and a minimal inference probe. Label any probe that may incur a small provider charge; key format or a successful model listing alone does not prove inference permission or available quota.
4. Select a supported model, approved projects, run budgets, concurrency limits, and any explicit fallback policy. Show model limitations relevant to QA.
5. Store only a masked credential label and secret reference in normal product records. Keep the key in a protected secret store; resolve it inside the provider gateway and keep it out of browser workers, prompts, artifacts, and analytics.
6. Offer Test connection, Rotate key, and Disconnect. Distinguish invalid credentials, missing model access, quota exhaustion, and transient provider outages. Product estimates do not replace the provider invoice or represent every use of a shared key elsewhere.

Subscription setup is a native-agent connection. Recommended first experiment: a customer-run companion that invokes the provider's official runtime. The runtime can receive bounded QA work and access narrowly scoped browser/repository tools; the control plane receives results and connection health. Keeping sign-in on the customer's runner is an architectural choice, not proof of provider permission or a requirement of every possible integration.

Proposed user flow: select supported subscription runtime → pair a local or private runner → start the official provider sign-in → complete it on the provider's page → return to the product → verify available account/runtime capabilities → run a small connection check. Provider credentials remain under the runtime's supported storage and refresh lifecycle. Our dashboard must not request passwords, pasted consumer session cookies, or copied authentication-cache files. A paired runner authenticates separately to our service using short-lived, project-scoped credentials.

A concrete OpenAI candidate is Codex app-server, which documents integration into other products, managed browser/device-code sign-in, authentication notifications, and account/rate-limit inspection. A Go companion could communicate with a pinned Codex process through local stdio JSON-RPC and relay sanitised state to the product. Avoid exposing the experimental WebSocket transport as our hosted public interface. Keep the provider runtime isolated per identity. These documented mechanisms establish a technical integration route; deployment suitability, plan eligibility, and commercial scope still need validation. [Codex app-server](https://learn.chatgpt.com/docs/app-server).

For Claude, an unmodified official runtime is only a conditional research candidate; exclude subscription connections from launch pending clarification of our exact offering. For Google, exclude consumer subscription connections from launch because of the explicit third-party access restrictions. Running on a customer's machine or wrapping an official executable must not be represented as automatically resolving permission. Neither provider should have a subscription-connect button in the launch UX. See section 22 for the verified sources and distinctions.

Connection cards should make operational differences understandable:

| Example connection | Ownership and execution | Useful status |
|---|---|---|
| Company release tests — API key | Workspace-authorised service connection; hosted execution | Ready for unattended runs; configured budget |
| My Codex connection — subscription | Personal connection; paired runner | Connected, runner online, supported model, quota when available |
| Private QA runner — subscription | Named authorised identity; customer infrastructure | Requires sign-in / quota reached / ready for supported tasks |

Do not silently share a personal subscription with teammates or convert it into a workspace pool. Product project permissions and provider entitlements both constrain who can use a connection. A model list is specific to the connection and runtime: connecting a subscription does not unlock every API model or identical capabilities across providers.

For pipeline execution, recommend a workspace API connection initially. Personal companion execution requires the machine to be awake, reachable, authenticated, and available; show this before accepting unattended use. A persistent private runner may address availability, but does not resolve provider eligibility on its own. Codex documents API keys as the automation default and Enterprise access tokens for certain trusted workflows; assess that enterprise path separately if customers need it. [Codex authentication](https://learn.chatgpt.com/docs/auth).

Both routes feed the same scenario, evidence, and reporting system, but require distinct adapters. Direct API calls run our own bounded agent loop; native runtime connections delegate to another agent harness. Version and evaluate the harness as well as the selected model. Enforce tool permissions, budgets, and assertion rules outside either model.

Use explicit connection states: connecting, ready, authentication required, quota exhausted, runner offline, provider unavailable, and disconnected. On quota exhaustion, pause work or use a preapproved fallback; never switch from subscription usage to paid API calls without the configured authorisation. Show remaining quota only when a supported provider interface exposes it. On disconnect, stop new work, apply a documented policy to in-flight work, revoke our runner lease, and invoke the provider's supported logout/revocation path where available.

Revised suggested delivery order: one direct API adapter powering both managed AI and customer-key connections for the first operational pipeline; a second provider to validate portability; a Codex native connection prototype during product validation; other native runtimes after their authentication, permissions, evidence capture, and unattended limitations are verified. This retains subscription support as a concrete product goal without making launch reliability depend on an unverified universal subscription bridge.

**21. Managed AI as the recommended onboarding default**

User proposal: offer our AI in addition to customer API keys and subscriptions. Recommendation: include this option and present it first, subject to pilot validation. This is a proposed product decision, not evidence that the user has approved a pricing model.

Managed AI means our hosted QA service pays for commercial model API usage and manages the model configuration. It does not require training a foundation model or imply ownership of the underlying models. The differentiated product remains the QA harness, context, rules, test execution, and evidence. Present the feature as managed AI or included AI rather than suggesting a proprietary foundation model exists.

Onboarding offers three choices:

| Choice | Customer experience | Billing relationship |
|---|---|---|
| Use included AI — recommended | Select an allowance/plan and start testing; use a tested default configuration | Customer pays our platform; we pay model-provider API costs |
| Bring an API key | Connect provider credentials and select a compatible model | Customer pays us for the platform and their provider for model usage |
| Connect a supported subscription — future, conditional | Pair and authenticate a native runtime only after its integration is explicitly permitted; Google and Claude excluded from launch | Customer pays us for the platform and maintains their eligible provider subscription |

For the initial segment, managed AI removes the need to create a developer billing account and makes unattended execution independent of a personal laptop. It also gives us a reference configuration for reproducing quality problems. These are expected advantages to validate, not measured conversion improvements or a promise that managed AI always produces better results.

Start with one benchmarked managed configuration; keep a compatible model-selection path for customers who want it. Disclose the provider/model used, retain it in run metadata, and honour project provider restrictions. Any automatic routing or fallback must stay within an explicit customer policy. Do not silently substitute a weaker model in a way that changes required verification quality merely because the allowance is nearly exhausted.

Use the existing direct-API adapter for both managed and customer-key modes. The Go backend resolves either a platform-owned or customer-owned credential reference after checking organisation, project, connection, and budget authorisation. Record who pays for each model call. Keep native subscription adapters separate because they execute another agent harness. Platform credentials must never be distributed to customers or browser/test workers.

Pricing recommendation: a platform subscription with a finite included AI allowance and defined browser/storage limits. Offer additional prepaid usage or explicitly enabled metered overage with a monthly ceiling. Present cost estimates before unusually broad discovery runs and show separate discovery, investigation, and regression usage. If using credits, explain their conversion and model-dependent consumption; do not hide unpredictable spending behind an opaque number. Determine prices from measured costs rather than guessing a token markup now.

Control costs per organisation and run: reserve estimated usage before dispatch; limit concurrent calls, output tokens, tool actions, retries, and elapsed time; reconcile reported usage; stop new work when allowance is exhausted. Allow for in-flight charges and bounded overshoot. Use provider-level controls as additional protection, not a substitute for our tenant-level ledger. OpenAI documents usage tracking, secret management, and spend controls for production API use. [OpenAI production guidance](https://developers.openai.com/api/docs/guides/production-best-practices).

Protect trial economics with a finite allowance, constrained concurrency, and progressive account verification appropriate to actual abuse. Avoid an unlimited autonomous-testing promise. Estimate contribution margin from inference, browser execution, artifact storage/egress, payment costs, and support. Measure heavy users and failure/retry cases, not just average successful runs.

Customer-visible behaviour when funds run low: finish already reserved work where possible, pause new AI work, and offer an explicit top-up or connection change. Deterministic tests can continue if their execution allowance remains available. A required AI assertion that could not run remains incomplete; a budget limit cannot turn a release gate green.

Enterprise considerations can remain incremental: workspace-owned managed connections, project restrictions, data-retention settings, provider/region selection where actually supported, and audit history. Verify the commercial API terms and data-processing setup for each managed provider before offering it; do not fund this mode by pooling consumer subscriptions or reselling native subscription sessions.

Pilot questions: does managed onboarding increase successful first runs; how much does a useful discovery cost; what is the cost of a healthy regression versus a difficult investigation; what allowance is understandable; and what fraction of teams still prefer BYOK? Keep the recommendation open to those results.

**22. Subscription policy verification and corrected launch recommendation**

Verified 26 September 2026 after the user raised account-suspension concerns. The earlier research established authentication mechanisms but did not adequately establish permission for Google's subscription route. Technical ability to sign in, run an executable, or obtain a token does not establish entitlement for a third-party commercial service.

Google's Gemini CLI terms notice explicitly prohibits direct third-party access to the services powering the CLI, including use of CLI OAuth in other tools, and warns of suspension or termination. The Antigravity FAQ similarly prohibits third-party access using its login and directs third-party-agent users toward API access. These official restrictions substantiate the concern; they do not establish that every affected person loses unrelated Google services. Do not claim that Gmail/Drive deletion is an inevitable or verified consequence. [Gemini CLI policy](https://geminicli.com/docs/resources/tos-privacy/), [Antigravity FAQ](https://www.antigravity.google/docs/faq/).

For Anthropic, its legal page distinguishes permitted hosting of an unmodified Claude Code binary under specified conditions from prohibited collection/intermediation of subscription credentials and offering our own Claude.ai login. Its SDK overview still requires prior approval for third-party subscription login/limits. This leaves our product-specific integration uncleared. [Claude legal conditions](https://code.claude.com/docs/en/legal-and-compliance), [Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview).

The June Help Center update says the proposed SDK billing change was paused. That is relevant billing guidance, not sufficient authorisation for every third-party integration. Do not rely on the withdrawn credit proposal elsewhere on that page as current policy. [Claude subscription billing update](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan).

Recommendation: managed AI funded through commercial APIs plus BYOK at launch. Defer Google and Claude subscription connections unless current written terms or provider confirmation clearly cover the exact architecture, identity ownership, hosted/customer-run deployment, unattended CI usage, and charging model. For Claude, seek product-specific written clarification because the relevant documentation is not fully aligned. Do not promise zero account-enforcement risk even for ordinary API integrations; they remain governed by their own terms and usage policies.

No provider has been contacted and no approval has been obtained. This is a correction to the recommended product scope, not a conclusion that every use of an official native runtime is forbidden. OpenAI's previously identified technical integration remains a separate feasibility item; this review does not certify it as risk-free or commercially cleared.
