package practices

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// MaxText bounds, in bytes, each text a result holds: a reason, a detail, a path, or a name.
	MaxText = 2000
	// MaxSecretFindings bounds the secret findings of one result. A scan of a repository that holds a flood of
	// credentials matches without end, and a result must fit the route it is reported on, so the findings beyond it
	// are left out and the result says so.
	MaxSecretFindings = 200
)

// Cut cuts text at MaxText bytes on a rune boundary, and leaves a shorter text as it is.
func Cut(text string) string {
	return cutAt(text, MaxText)
}

// cutAt cuts text at limit bytes on a rune boundary, and leaves a shorter text as it is.
func cutAt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// idDigestBytes is how many bytes of the SHA-256 of an ID a clipped ID ends with, written in hex.
const idDigestBytes = 8

// clipID bounds a finding's ID to MaxText bytes. An ID that fits stays as it is, because acknowledgements name it. A
// longer one is cut on a rune boundary and ends with `~` and a short digest of the whole ID, so two IDs that differ only
// past the cut stay two IDs, and the same ID is clipped to the same text each time.
func clipID(id string) string {
	if len(id) <= MaxText {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	tail := "~" + hex.EncodeToString(sum[:idDigestBytes])
	return cutAt(id, MaxText-len(tail)) + tail
}

// Clip writes text on one line, cut at MaxText bytes on a rune boundary.
func Clip(text string) string {
	return Cut(strings.Join(strings.Fields(text), " "))
}

// capSecrets keeps the first MaxSecretFindings secret findings, in the order given, and every other finding. It says
// whether any were left out.
func capSecrets(findings []Finding) ([]Finding, bool) {
	secrets := 0
	for _, f := range findings {
		if f.Practice == Secrets {
			secrets++
		}
	}
	if secrets <= MaxSecretFindings {
		return findings, false
	}
	kept := make([]Finding, 0, len(findings)-(secrets-MaxSecretFindings))
	seen := 0
	for _, f := range findings {
		if f.Practice == Secrets {
			if seen++; seen > MaxSecretFindings {
				continue
			}
		}
		kept = append(kept, f)
	}
	return kept, true
}

// Bound returns the result with each text bounded and the secret findings capped, whatever made it. A reason and a
// detail are written on one line and cut at MaxText bytes, every other text is cut there except a finding's ID, which
// a digest of the whole ID keeps unique, and the secret findings past MaxSecretFindings are dropped with Truncated set. A control plane bounds what a worker reports, and does not trust
// the worker to have. It changes nothing it was given.
func Bound(r Result) Result {
	r.Repository, r.Commit, r.BaseBranch, r.Worker, r.ScannedFrom = Cut(r.Repository), Cut(r.Commit), Cut(r.BaseBranch), Cut(r.Worker), Cut(r.ScannedFrom)
	r.Practices = slices.Clone(r.Practices)
	for i := range r.Practices {
		r.Practices[i].Reason = Clip(r.Practices[i].Reason)
	}
	r.Findings = slices.Clone(r.Findings)
	for i := range r.Findings {
		f := &r.Findings[i]
		f.Detail = Clip(f.Detail)
		f.ID, f.Subject, f.Rule, f.Path, f.Commit, f.Digest = clipID(f.ID), Cut(f.Subject), Cut(f.Rule), Cut(f.Path), Cut(f.Commit), Cut(f.Digest)
	}
	var cut bool
	if r.Findings, cut = capSecrets(r.Findings); cut {
		r.Truncated = true
	}
	return r
}

// Validate refuses a result no worker makes: a practice the table does not hold, one twice, a status or a severity no
// practice has, and a finding of a practice the table does not hold. A result on the current table version holds a
// status for every practice of the table. A result on an earlier version may lack the practices a later version
// added, and nothing else: it always holds a status for secrets.
func (r Result) Validate() error { return validate(r, table, TableVersion) }

// validate is Validate against a table at a version.
func validate(r Result, rows []Practice, current int) error {
	seen := map[ID]bool{}
	for _, p := range r.Practices {
		if !slices.ContainsFunc(rows, func(row Practice) bool { return row.ID == p.ID }) {
			return fmt.Errorf("the result holds a status for %q, which is no practice", p.ID)
		}
		if seen[p.ID] {
			return fmt.Errorf("the result holds a status for %s twice", p.ID)
		}
		seen[p.ID] = true
		switch p.Status {
		case StatusMet, StatusFinding, StatusUnknown, StatusNotApplicable:
		default:
			return fmt.Errorf("the status of %s is %q, which is no status", p.ID, p.Status)
		}
	}
	behind := r.TableVersion >= 1 && r.TableVersion < current
	for _, p := range rows {
		if !seen[p.ID] && (p.ID == Secrets || !behind || p.Since <= r.TableVersion) {
			return fmt.Errorf("the result holds no status for %s", p.ID)
		}
	}
	for _, f := range r.Findings {
		if !slices.ContainsFunc(rows, func(row Practice) bool { return row.ID == f.Practice }) {
			return fmt.Errorf("the finding %q is of %q, which is no practice", f.ID, f.Practice)
		}
		switch f.Severity {
		case High, Medium, Low:
		default:
			return fmt.Errorf("the finding %q has the severity %q, which is no severity", f.ID, f.Severity)
		}
	}
	return nil
}
