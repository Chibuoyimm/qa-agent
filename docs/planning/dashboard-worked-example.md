# Worked example: testing a SaaS dashboard thoroughly

Companion to `research-and-product-plan.md`. This is a proposed product specification using an invented application and synthetic data. None of these tests has been run. The rules are illustrative and must be confirmed for each real customer.

**The request**

“Test the dashboard. Make sure the numbers are correct, filters work, and users cannot see another organisation's data. Give me a short recording of anything broken.”

**What the agent must establish first**

The agent finds the dashboard route, revenue/count cards, date and organisation filters, a chart, and an export action. Frontend context links them to an aggregate endpoint. Backend context, when available, suggests which order statuses and refund events are included.

It proposes a readable rule: “Net revenue means captured payments during the selected period, minus processed refunds during that period, for the selected organisation and currency.” This is a candidate, not a fact inferred solely from a label or implementation.

The product owner confirms this example rule. They also confirm that payment capture time and refund processing time determine period membership independently; periods use the organisation's configured timezone; start is inclusive and end exclusive; the metric uses NGN only; prior-period comparison uses the corresponding full previous calendar month for this preset; and a zero previous value displays '—' rather than infinity.

These details matter. Some real products recognise revenue differently, attribute refunds to the original sale period, combine currencies, use a rolling comparison, or deliberately hide sensitive totals from some roles. The tool cannot choose these semantics on the customer's behalf.

**The controlled fixture**

Test organisation: Acme Test. Organisation timezone: Africa/Lagos. Currency: NGN. Selected period: September 2026, expressed as `[2026-09-01 00:00 WAT, 2026-10-01 00:00 WAT)`. Compare with August 2026. Pin the test clock after the selected period if supported; otherwise use explicit historical dates and record the environment clock.

Amounts below are displayed in naira for readability. The executable check uses integer minor units or exact decimal arithmetic, not floating-point arithmetic.

| Record | Organisation | State / timing | Amount | Expected contribution |
|---|---|---|---:|---:|
| Payment A | Acme Test | Captured in September | ₦100,000 | +₦100,000 |
| Payment B | Acme Test | Captured in September | ₦50,000 | +₦50,000 |
| Refund A1 | Acme Test | Processed in September against A | ₦10,000 | −₦10,000 |
| Order C | Acme Test | Pending, never captured | ₦70,000 | ₦0 |
| Order D | Acme Test | Cancelled, never captured | ₦90,000 | ₦0 |
| Payment E | Other Test | Captured in September | ₦500,000 | ₦0 |
| Payment F | Acme Test | Captured in August | ₦100,000 | September: ₦0; August: +₦100,000 |

Expected September net revenue: **₦140,000**. Expected August net revenue: **₦100,000**. Expected period increase: **40%**. Captured payment count is **2** if the approved count rule counts captures and does not subtract refunds.

Preparation returns record IDs and confirms the dataset exists with the expected statuses. The expected values above come from the fixture and approved rule, not the dashboard aggregate endpoint. If preparation fails or another run modifies the same dataset, the numerical test is blocked or invalidated rather than confidently evaluated.

**The scenario inventory**

| ID | Plain-language scenario | Expected result / evidence | Special prerequisite |
|---|---|---|---|
| DASH-01 | Dashboard loads for a permitted user | Required cards and controls become usable; loading states complete within the agreed bound | Valid member session |
| DASH-02 | Revenue reflects completed payments and refunds | Exact net revenue is ₦140,000, supported by the fixture and rule | Controlled fixture |
| DASH-03 | Unpaid or cancelled orders do not inflate revenue | Pending and cancelled records contribute zero | Distinct fixture records |
| DASH-04 | September figures exclude August payments | September is ₦140,000; August is ₦100,000 | Explicit period selection |
| DASH-05 | Period comparison uses the correct formula | `(140000 − 100000) / 100000 = 40%` | Confirmed comparison rule |
| DASH-06 | A legitimate empty period displays zero clearly | Zero is shown after a successful data response, with an appropriate empty chart | Empty period |
| DASH-07 | A failed revenue request is not presented as a valid zero | Error/retry state appears; stale or absent values are labelled according to the rule | Controlled response failure |
| DASH-08 | Date filters update every relevant widget | Cards, chart, and export use the same selected interval and scope | Distinct records in each interval |
| DASH-09 | A user cannot see another organisation's data | Other Test payment never contributes or appears; authorised scope is enforced | Two test organisations |
| DASH-10 | A revoked user cannot retrieve dashboard data | Old session/deep link loses access according to the revocation policy | Second user and revoke capability |
| DASH-11 | A new capture changes the total and survives reload | Adding ₦25,000 increases total to ₦165,000 within the agreed update window | Safe creation hook or UI flow |
| DASH-12 | A refund updates the total once | An additional ₦5,000 processed refund decreases baseline total to ₦135,000, including after reload | Isolated reset fixture |
| DASH-13 | Chart and export reconcile with the agreed metric | Bucket sums and exported values match the same eligible dataset | Confirmed chart/export semantics |
| DASH-14 | Period boundaries respect organisation timezone | Immediately-before, exactly-at-start, just-before-end, and exactly-at-end cases follow the interval rule | Boundary fixture |
| DASH-15 | A zero prior period does not produce a misleading percentage | Approved '—' state is shown; no infinity/NaN | Zero-prior fixture |
| DASH-16 | Rapid filter changes cannot display the wrong period's response | Final state reflects the latest selection even if an earlier response arrives later | Controlled latency/order |
| DASH-17 | Dashboard remains usable on a narrow screen and keyboard | Important amounts remain readable and filter controls operable | Target viewport and keyboard steps |
| DASH-18 | Refresh and navigation preserve the agreed filter behaviour | URL, controls, and fetched data remain consistent with the product's chosen persistence rule | Explicit filter-persistence rule |

These cases illustrate discovery depth. They are not an exhaustive checklist for every dashboard. Confirm which widgets share semantics before asserting that all chart totals equal the revenue card.

DASH-11 and DASH-12 each start from an isolated baseline. Their expected values are not sequential instructions to modify a shared dataset. DASH-09 needs more than hiding an organisation selector: where authorised, attempt a direct resource/API request using the restricted user's credentials. If only UI visibility was tested, report only that proof.

**One scenario as a reviewable contract**

Scenario DASH-02 revision 1:

- Purpose: a permitted user sees the correct net revenue for the selected organisation and month.
- Actor: Acme Test member with revenue-view permission.
- Setup: fixture version `dashboard-ngn-v1`; data readiness verified; September 2026 selected.
- Actions: log in, open dashboard, select Acme Test and the explicit September interval, wait for the relevant result to settle within the approved deadline.
- Expected: net revenue is exactly ₦140,000 with the NGN presentation; no loading/error state is represented as a successful numerical result.
- Basis: approved rule `net-revenue-v1`, fixture receipt, and independent integer calculation.
- Evidence: actual displayed value, screenshot, relevant request/response, timestamp, account/organisation identity, assertion result, deployed revision information, and recording segment.
- Result policy: wrong value → failed; missing or invalid fixture → blocked; ambiguous read or unsupported observation → inconclusive; exact verified value → passed for this scenario and dataset.
- Cleanup: remove run-owned resources or retain only under an explicit pilot retention policy; report cleanup outcome independently.

The frontend test should still exercise the page normally. Preparing unrelated records through a fixture API is efficient; directly changing the page DOM to make the expected number appear would invalidate the test.

**What happens with only the frontend repository**

The agent can still discover UI states, observe network requests, test filters, validate response presentation, and verify user-observable outcomes against a known fixture supplied through an API or the UI. Backend source is helpful but not a prerequisite for all business checks.

If there is no controlled fixture or independent source of truth, the report must narrow the claim:

“The dashboard displayed ₦140,000, matching the API response. I could not independently verify which payments or refunds should be included.”

It must not rewrite that as “revenue calculation passed.”

**Example failure report**

Title: “September revenue includes cancelled orders.”

Expected: ₦140,000, from the approved fixture and revenue rule.

Observed: ₦230,000 on both the dashboard and its aggregate response.

Reproduction: sign in as the fixture member, select Acme Test, choose September 2026, and inspect net revenue. Repeated once with a reset fixture and the same result.

Evidence: screenshot, assertion output, request/response, fixture receipt, deployment identity, and a 32-second recording.

Diagnosis: “The difference is ₦90,000, equal to the cancelled order in this fixture. Incorrect status filtering is a plausible cause; the runtime evidence does not yet prove the exact code path.”

Impact: the dashboard overstates revenue for this tested dataset. Severity should reflect the customer's use of the metric and whether the defect is limited to this view or affects downstream reporting.

This demonstrates why API agreement alone is insufficient: the UI and API can agree on ₦230,000 while both disagree with the approved business expectation.

**How to test whether the QA agent is useful**

Create controlled variants of this example during a future prototype:

| Variant | Purpose |
|---|---|
| Healthy implementation | Check for false alarms and repeatability |
| Aggregate includes cancelled order | Verify detection beyond UI/API agreement |
| Refund subtraction omitted | Verify independent numerical assertion |
| UI hardcodes zero on API error | Verify distinction between empty and failed state |
| Server omits organisation scoping | Verify cross-tenant test and its evidence |
| Wrong timezone boundary | Verify boundary fixtures |
| Older filter response wins a race | Verify adversarial interaction timing |
| Success toast without persisted mutation | Verify postcondition beyond immediate feedback |
| Harmless markup/label change | Assess locator maintenance without weakening the expectation |
| Wrong metric already present before discovery | Detect whether the agent learns a defect as the desired baseline |

Freeze accepted rules before running defect variants. Keep some defect implementations hidden from the agent and prompt developers. Track detection, false passes, false alarms, cost, elapsed time, and human clarification. The product is useful only if it reliably distinguishes these cases while explaining its limits.

**The plain-language result view**

Illustrative output, not an actual run:

“Dashboard: 18 scenarios agreed. Twelve completed: ten passed and two failed. Three need additional test data, one needs a revoked-user account, and two have not run. The run found an incorrect revenue total and a filter race. Exact arithmetic was checked against controlled data; permission revocation remains unverified.”

Show the list beneath that summary so users can inspect the denominator, add missing scenarios, and understand what setup is needed. A short video makes the finding easier to see. The recorded assertion and its independent expected value make the finding defensible.
