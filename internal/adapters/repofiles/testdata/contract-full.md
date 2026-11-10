+++
kind = "contract"
id = "checkout-retry"
title = "Retry failed checkout calls"
plan = "payments-v2"
milestone = "m1-reliable-checkout"
shared = true
uses = ["payments-client"]
documents = ["spec:checkout-api", "architecture:payments"]
decisions = []

[[criteria]]
id = "c1"
text = "A failed call retries twice with the same idempotency key"
requirements = ["PAY-12"]

[[criteria]]
id = "c2"
text = "The retry budget bounds every call"
requirements = ["PAY-13", "PAY-12"]

[[work_orders]]
id = "retry-budget"
title = "Add the retry budget"
repo = "."
priority = 2
size = "medium"
depends_on = []
needs = { driver = "claude-code" }
+++

The statement of what this work must deliver.
