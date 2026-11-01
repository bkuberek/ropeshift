package authority

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrPolicyDigest means a policy's body is not what its digest covers.
var ErrPolicyDigest = errors.New("a policy body does not match its digest")

// PolicyKind names what a policy governs.
type PolicyKind string

// PolicyDecision governs what an orchestrator may decide and assume. The other kinds arrive
// with the units that read them.
const PolicyDecision PolicyKind = "decision"

// PolicyVersion is one version of a policy, which a member can review (FR-ORCH-5). Body is
// the table as its author wrote it, and Digest covers those bytes. Settings is the same
// table in JSON, so the control plane reads it with no TOML parser.
type PolicyVersion struct {
	ID         string     `json:"id"`
	Kind       PolicyKind `json:"kind"`
	Version    int64      `json:"version"`
	Digest     string     `json:"digest"`
	Body       string     `json:"body"`
	Settings   string     `json:"settings"`
	AuthoredBy string     `json:"authored_by"`
	CreatedAt  int64      `json:"created_at"`
}

// PolicyDigest is the digest of a policy's body.
func PolicyDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// CheckDigest refuses a body that is not what its digest covers, so no decision can record
// a version whose text changed after a member reviewed it.
func (p PolicyVersion) CheckDigest() error {
	if p.Digest != PolicyDigest(p.Body) {
		return fmt.Errorf("%w: %s version %d", ErrPolicyDigest, p.Kind, p.Version)
	}
	return nil
}
