package control

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The signed webhook deliveries of CI events (FR-SPEND-20) are hints, as intake treats a tracker's: a delivery is
// verified against the secret of the repository it names, before anything of it is trusted; dropped when its ID has
// come before; and turned into a hint for the head it names, which carries no state, because the read decides what
// changed. Polling works alone, so a delivery that is refused, lost, or late costs only the delay
// (https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries).

// The reasons a delivery is refused or dropped, which Counts reports.
const (
	RefusedMissingSignature  = "missing_signature"
	RefusedBadSignature      = "bad_signature"
	RefusedUnknownRepository = "unknown_repository"
	RefusedNoSecret          = "no_secret"
	RefusedMalformed         = "malformed"
	RefusedOversized         = "oversized"
	RefusedContentType       = "content_type"
	DroppedReplay            = "replayed"
)

const (
	// CIDeliveriesHeld is how many delivery IDs are remembered, the oldest forgotten first.
	CIDeliveriesHeld = 4096
	// ciDeliveryLife is how long an ID is remembered. A redelivery later than this is read again, which costs a read.
	ciDeliveryLife = time.Hour
	// refusalWarnEvery is how many refusals of one reason pass between two warnings, after the first.
	refusalWarnEvery = 100
)

// CIOutcome is what a delivery that was not refused came to.
type CIOutcome string

const (
	// CIAccepted made a hint.
	CIAccepted CIOutcome = "accepted"
	// CIIgnored was a delivery of an event or an action that says nothing of CI to read.
	CIIgnored CIOutcome = "ignored"
	// CIDuplicate was a delivery whose ID had come before.
	CIDuplicate CIOutcome = "duplicate"
	// CIPong answered GitHub's ping of a new webhook.
	CIPong CIOutcome = "pong"
)

// CIRegistration is one repository a project holds that a delivery may name, and the variable of the server's
// environment that holds the key its deliveries are signed with.
type CIRegistration struct {
	Binding   ledger.ForgeBinding
	SecretEnv string
}

// CIRepositories finds the registered repositories that a host, an owner, and a name name, in every active project,
// without regard to case, as GitHub names them.
type CIRepositories interface {
	CIRepository(ctx context.Context, host, owner, repo string) ([]CIRegistration, error)
}

// CIDelivery is one webhook delivery as it arrived: the headers X-GitHub-Event, X-GitHub-Delivery,
// X-Hub-Signature-256, and X-GitHub-Enterprise-Host, and the body the signature is of.
type CIDelivery struct {
	Event, ID, Signature, EnterpriseHost string
	Body                                 []byte
}

// CIRefusal is a delivery that was not taken. Malformed says its signature held and it was still no delivery of
// GitHub's, which the caller may be told. Every other refusal looks the same to a caller, so a caller learns nothing
// of which repositories there are.
type CIRefusal struct {
	Reason    string
	Malformed bool
}

func (e *CIRefusal) Error() string { return "the webhook delivery is refused: " + e.Reason }

// CIWebhooks takes the signed webhook deliveries of CI events.
type CIWebhooks struct {
	repos CIRepositories
	hints *CIHints
	env   func(string) (string, bool)
	now   func() time.Time
	warn  func(string)

	mu     sync.Mutex
	counts map[string]int64
	seen   map[string]time.Time
	order  []string
}

// NewCIWebhooks returns the use case. Env reads the server's environment, where the keys are, and hints is the board
// the hints go on. Warn hears the first refusal of each reason, and each hundredth after it, in words that name no
// repository, no key, and no variable.
func NewCIWebhooks(repos CIRepositories, hints *CIHints, env func(string) (string, bool), now func() time.Time, warn func(string)) *CIWebhooks {
	if env == nil {
		env = func(string) (string, bool) { return "", false }
	}
	if warn == nil {
		warn = func(string) {}
	}
	return &CIWebhooks{repos: repos, hints: hints, env: env, now: now, warn: warn, counts: map[string]int64{}, seen: map[string]time.Time{}}
}

// Counts lists how many deliveries each reason refused or dropped.
func (c *CIWebhooks) Counts() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.counts)
}

// Count counts a refusal made before a delivery reached Receive, such as a body too large to read.
func (c *CIWebhooks) Count(reason string) { c.refuse(reason, false) }

func (c *CIWebhooks) refuse(reason string, malformed bool) error {
	c.mu.Lock()
	c.counts[reason]++
	n := c.counts[reason]
	c.mu.Unlock()
	if n == 1 || n%refusalWarnEvery == 0 {
		c.warn(fmt.Sprintf("a GitHub webhook delivery was refused (%s); %d for that reason so far", reason, n))
	}
	return &CIRefusal{Reason: reason, Malformed: malformed}
}

// payload is what a delivery says that a hint is made of.
type payload struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	CheckSuite struct {
		HeadSHA string `json:"head_sha"`
	} `json:"check_suite"`
	CheckRun struct {
		HeadSHA string `json:"head_sha"`
	} `json:"check_run"`
	WorkflowRun struct {
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_run"`
}

var commitID = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// Receive takes one delivery. It reads the body only to find the repository it names, which says whose key to
// verify it with, and trusts nothing it reads until the signature has held for that repository.
func (c *CIWebhooks) Receive(ctx context.Context, d CIDelivery) (CIOutcome, error) {
	if d.Signature == "" {
		return "", c.refuse(RefusedMissingSignature, false)
	}
	var body payload
	if json.Unmarshal(d.Body, &body) != nil {
		return "", c.refuse(RefusedUnknownRepository, false)
	}
	host := "github.com"
	if d.EnterpriseHost != "" {
		host = strings.ToLower(d.EnterpriseHost)
	}
	owner, repo, named := strings.Cut(body.Repository.FullName, "/")
	if !named || strings.Contains(repo, "/") {
		return "", c.refuse(RefusedUnknownRepository, false)
	}
	registered, err := c.repos.CIRepository(ctx, host, owner, repo)
	if err != nil {
		return "", err
	}
	if len(registered) == 0 {
		return "", c.refuse(RefusedUnknownRepository, false)
	}
	bindings, keyed := c.verified(registered, d)
	if !keyed {
		return "", c.refuse(RefusedNoSecret, false)
	}
	if len(bindings) == 0 {
		return "", c.refuse(RefusedBadSignature, false)
	}
	if d.Event == "" || d.ID == "" {
		return "", c.refuse(RefusedMalformed, true)
	}
	if !c.firstSight(d.ID) {
		c.mu.Lock()
		c.counts[DroppedReplay]++
		c.mu.Unlock()
		return CIDuplicate, nil
	}
	head, outcome := "", CIAccepted
	switch {
	case d.Event == "ping":
		return CIPong, nil
	case d.Event == "check_suite" && body.Action == "completed":
		head = body.CheckSuite.HeadSHA
	case d.Event == "check_run" && body.Action == "completed":
		head = body.CheckRun.HeadSHA
	case d.Event == "workflow_run" && slices.Contains([]string{"requested", "in_progress", "completed"}, body.Action):
		head = body.WorkflowRun.HeadSHA
	default:
		return CIIgnored, nil
	}
	if !commitID.MatchString(head) {
		head = ""
	}
	for _, binding := range bindings {
		c.hints.Note(binding, head)
	}
	return outcome, nil
}

// verified lists the bindings whose own key verifies the delivery's signature, each once, and says whether any of
// the registrations had a key to verify with.
func (c *CIWebhooks) verified(registered []CIRegistration, d CIDelivery) ([]ledger.ForgeBinding, bool) {
	var bindings []ledger.ForgeBinding
	keyed := false
	for _, registration := range registered {
		if registration.SecretEnv == "" {
			continue
		}
		key, set := c.env(registration.SecretEnv)
		if !set || key == "" {
			continue
		}
		keyed = true
		if signatureHolds(key, d.Body, d.Signature) && !slices.Contains(bindings, registration.Binding) {
			bindings = append(bindings, registration.Binding)
		}
	}
	return bindings, keyed
}

// signatureHolds says the signature is the HMAC-SHA256 of the body under the key, as GitHub writes it in
// X-Hub-Signature-256: "sha256=" and the digest in hex. The digests are compared in constant time.
func signatureHolds(key string, body []byte, signature string) bool {
	digest, found := strings.CutPrefix(signature, "sha256=")
	if !found {
		return false
	}
	given, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), given)
}

// firstSight remembers a delivery ID and says whether it is new, within the life of an ID and the number of IDs held.
func (c *CIWebhooks) firstSight(id string) bool {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.order) > 0 {
		at, held := c.seen[c.order[0]]
		if held && now.Sub(at) < ciDeliveryLife && len(c.order) < CIDeliveriesHeld {
			break
		}
		delete(c.seen, c.order[0])
		c.order = c.order[1:]
	}
	if at, held := c.seen[id]; held && now.Sub(at) < ciDeliveryLife {
		return false
	}
	if _, held := c.seen[id]; !held {
		c.order = append(c.order, id)
	}
	c.seen[id] = now
	return true
}
