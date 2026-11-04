+++
kind = "contract"
id = "checkout-retry"
title = "Retry failed checkout calls"
requirements = ["PAY-12"]
decisions = []

[[criteria]]
id = "c1"
text = "A failed call retries twice with the same idempotency key"

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
