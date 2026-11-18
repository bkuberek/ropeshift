package api

// GitHubWebhookPath is the route GitHub's webhook deliveries of CI events go to (FR-SPEND-20). It takes no session
// and no credential: the signature of each delivery, made with the key of the repository it names, is its authority.
const GitHubWebhookPath = "/v1/webhooks/github"

// CodeBadSignature is the answer to every delivery the route cannot verify: no signature, a signature that does not
// hold, a repository ropeshift does not hold, and a repository that has no key. Each answers the same, so a caller
// learns nothing of which repositories there are.
const CodeBadSignature = "bad_signature"
