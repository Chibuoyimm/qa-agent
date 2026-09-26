# Synthetic SaaS sample

Run `npm start` in this directory, then open `http://127.0.0.1:4174`. The sample listens on port 4174. Its login is intentionally synthetic: `demo@example.test` / `pass1234`. Override these with `QA_TEST_EMAIL` and `QA_TEST_PASSWORD` in both the sample and worker environment. Do not use real customer credentials.

Start with `QA_SAMPLE_DEFECT=1 npm start` to inject the revenue defect. The seeded orders are paid 150000, refunded 10000, and cancelled 90000. Correct net revenue is **140000**. The defect reports **230000** from the sample API and displays that same incorrect value in the UI. A useful QA scenario must assert the independent expected value `140000`, so matching the UI to its own API cannot hide the defect.

Stable Playwright test IDs are `login-email`, `login-password`, `login-submit`, `login-error`, `dashboard-revenue`, `dashboard-orders`, `signout`, and `order-<order id>-<id|state|amount>` (for example, `order-ord-refund-amount`). [scenarios.json](scenarios.json) contains six approved scenario inputs for API seeding. Example approved scenario steps:

```json
[
  {"action":"navigate","path":"/login"},
  {"action":"fill","test_id":"login-email","secret_env":"QA_TEST_EMAIL"},
  {"action":"fill","test_id":"login-password","secret_env":"QA_TEST_PASSWORD"},
  {"action":"click","test_id":"login-submit"},
  {"action":"assert_text","test_id":"dashboard-revenue","value":"140000"},
  {"action":"assert_text","test_id":"dashboard-orders","value":"3 orders"}
]
```

`signout` is the state-changing logout control. Both login and logout use POST. The dashboard API requires the session cookie. A fresh browser context for each scenario keeps sessions separate.
