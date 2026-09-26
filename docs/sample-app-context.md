# Sample application context for AI proposals

This is synthetic application evidence that may be copied into the proposal form. Ask: “Propose thorough checks for the dashboard, login and signout. Use independent expected totals and identify any missing information.”

## Known business rules

The target is the sample in this repository. Its origin is configured separately in the project. Each scenario begins in a new browser session. Unauthenticated users see the login form at `/`. Authentication submits that form and navigates to `/dashboard`. Signing out revokes the session and redirects to the login form; revisiting `/dashboard` must require login again.

The fixed dataset is one paid order of 150000, a refund of 10000, and one cancelled order of 90000. Approved net revenue is paid minus refunds, excluding cancelled orders: 140000. There are three order records. The defect mode incorrectly adds the cancelled order, yielding 230000. Treat the business rule here as the expectation, not the current API response.

## Browser controls

| Test ID | Meaning |
|---|---|
| `login-email` | Email input; fill from worker environment reference `QA_TEST_EMAIL` |
| `login-password` | Password input; fill from reference `QA_TEST_PASSWORD` |
| `login-submit` | Submit login |
| `login-error` | Invalid-login message, exact text `Invalid test credentials` |
| `dashboard-revenue` | Exact text `140000` on a healthy build |
| `dashboard-orders` | Exact text `3 orders` |
| `order-ord-refund-state` | Exact text `refunded` |
| `order-ord-refund-amount` | Exact text `10000` |
| `order-ord-cancelled-state` | Exact text `cancelled` |
| `order-ord-cancelled-amount` | Exact text `90000` |
| `signout` | Submit signout |

Use the declared secret references, never literal real credentials. For an invalid-login check, `nobody@example.test` and `deliberately-invalid` are synthetic invalid values. The current pilot supports only navigate, fill, click, exact-text assertion and visibility assertion. There is no order-creation form, role-management UI, account provisioning or payment integration in this sample. Ask questions for unsupported journeys rather than inventing selectors or behaviour.
