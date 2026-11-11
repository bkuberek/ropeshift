+++
kind = "requirements"
id = "payments"
plan = "checkout-plan"

[[requirement]]
id = "PAY-11"
text = "Every payment call goes through one client"
status = "met"
evidence = ["api:internal/client.go", "api:internal/client_test.go"]

[[requirement]]
id = "PAY-12"
text = "A failed call retries with the same idempotency key"
status = "open"
+++

Requirements, one of which the code meets.
