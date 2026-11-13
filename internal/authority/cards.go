package authority

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
)

// MaxWhy bounds the reason a delegate gives for a card, in bytes.
const MaxWhy = 2000

// CardStates are the states a confirmation card passes through, in the order a person reads them: open,
// running, confirmed, failed to run, declined, void, and expired.
func CardStates() []ApprovalState {
	return []ApprovalState{ApprovalPending, ApprovalApproved, ApprovalUsed, ApprovalFailed, ApprovalDenied, ApprovalVoid, ApprovalExpired}
}

// CardDetail is what a confirmation card holds of the operation it runs: the operation and its label, the
// exact HTTP call as the delegate sent it, the delegate's reason, and what the call changes. A member's
// confirmation replays this call, and nothing else.
type CardDetail struct {
	Operation string          `json:"operation"`
	Label     string          `json:"label"`
	Method    string          `json:"method"`
	Path      string          `json:"path"`
	Arguments json.RawMessage `json:"arguments"`
	Why       string          `json:"why"`
	Changes   json.RawMessage `json:"changes"`
}

// FieldChange is one field of a record a card changes, read from the record as it stood. ArgumentChange is
// one argument of a call that names no record to read, and BodyChange a whole body a card replaces.
type (
	FieldChange struct {
		Field string `json:"field"`
		From  any    `json:"from"`
		To    any    `json:"to"`
	}
	ArgumentChange struct {
		Field string `json:"field"`
		To    any    `json:"to"`
	}
	BodyChange struct {
		Before string `json:"before"`
		After  string `json:"after"`
	}
)

// CardDetailOf reads a card's detail.
func CardDetailOf(a Approval) (CardDetail, error) {
	var detail CardDetail
	if a.Kind != KindConfirm {
		return detail, fmt.Errorf("%w: %s is no card", ErrBadApproval, a.ID)
	}
	if err := json.Unmarshal([]byte(a.Detail), &detail); err != nil {
		return detail, fmt.Errorf("%w: card %s: %v", ErrBadApproval, a.ID, err)
	}
	return detail, nil
}

func digestOf(parts ...string) string {
	sum := sha256.New()
	for i, part := range parts {
		if i > 0 {
			sum.Write([]byte{0})
		}
		sum.Write([]byte(part))
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// RecordDigest binds a card to a record as it stands: its kind, its ID, and the revision of the newest
// event about it. Any change to the record moves that revision.
func RecordDigest(kind, id string, rev int64) string {
	return digestOf(kind, id, strconv.FormatInt(rev, 10))
}

// RequestDigest binds a card that decides a request to that request's own digest and state.
func RequestDigest(request Approval) string {
	return digestOf(SubjectApproval, request.ID, request.SubjectDigest, string(request.State))
}

// PolicyBinding binds a card that publishes a policy to the version of its kind in force.
func PolicyBinding(kind PolicyKind, inForce string) string {
	return digestOf("policy", string(kind), inForce)
}

// ArgumentsDigest binds a card for an operation that creates its subject to the arguments it creates it
// from. Nothing else can change under it, so such a card never goes void.
func ArgumentsDigest(canonical string) string {
	return digestOf("arguments", canonical)
}
