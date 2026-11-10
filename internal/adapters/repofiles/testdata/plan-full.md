+++
kind = "plan"
id = "payments-v2"
title = "Payments v2"
goal = "reliable-checkout"
documents = ["requirements:payments", "spec:checkout-api", "architecture:payments", "experience-design:checkout-flow", "milestone:m1-reliable-checkout"]

[[decision]]
id = "d1"
question = "Which payment provider do we integrate first?"
answer = "The one the team already has an account with."
rationale = "It avoids a procurement step before the first milestone."
blocking = true

[[decision]]
id = "d2"
question = "Do retries use exponential backoff?"
answer = "Yes, with jitter."
+++

How the payments work is planned.
