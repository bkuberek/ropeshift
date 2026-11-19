package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
)

// SecretMatch is one match of a secret rule in a text. It holds where the match is and the digest of the matched text,
// and never the text.
type SecretMatch struct {
	Rule string `json:"rule"`
	// Start and End are the byte offsets of the matched text, End exclusive. A rule that keeps the words around a
	// value matches only the value.
	Start int `json:"start"`
	End   int `json:"end"`
	// Digest is the hex SHA-256 of the matched text.
	Digest string `json:"digest"`
}

// SecretRuleNames lists the rules the scan for secrets applies, in the order it applies them.
func SecretRuleNames() []string {
	var names []string
	for _, rule := range credentialRules {
		if rule.secret {
			names = append(names, rule.name)
		}
	}
	return names
}

// FindSecrets returns the matches of the secret rules in text, in the order they sit in it. A rule that comes first
// takes the text it matches: a match inside the span of an earlier match is part of it, and is not found again.
func FindSecrets(text string) []SecretMatch {
	return findSecrets(text, func(redactionRule) bool { return true }, false)
}

// FindBlockSecrets returns the matches of the secret rules that match text spanning lines, such as a private key,
// in the order they sit in text. It is FindSecrets for the rules a change to some lines of the text can break, and
// it leaves out a block that never ends, which holds every line after its start.
func FindBlockSecrets(text string) []SecretMatch {
	return findSecrets(text, func(rule redactionRule) bool { return rule.block }, true)
}

func findSecrets(text string, applies func(redactionRule) bool, closedOnly bool) []SecretMatch {
	var found []SecretMatch
	for _, rule := range credentialRules {
		if !rule.secret || !applies(rule) {
			continue
		}
		matches := newMatches(rule, text, found)
		if closedOnly && rule.closed != nil {
			matches = slices.DeleteFunc(matches, func(m SecretMatch) bool { return !rule.closed(text[m.Start:m.End]) })
		}
		found = mergeMatches(found, matches)
	}
	return found
}

// newMatches returns the matches of one rule in text that overlap no match of earlier, which is sorted and holds no
// two overlapping matches. A rule's own matches are in order and apart, so one walk along earlier finds each overlap.
func newMatches(rule redactionRule, text string, earlier []SecretMatch) []SecretMatch {
	var matches []SecretMatch
	next := 0
	for _, loc := range rule.pattern.FindAllStringSubmatchIndex(text, -1) {
		start, end := loc[0], loc[1]
		if rule.group != 0 {
			start, end = loc[2*rule.group], loc[2*rule.group+1]
		}
		if start < 0 || (rule.skip != nil && rule.skip(text[start:end])) {
			continue
		}
		for next < len(earlier) && earlier[next].End <= start {
			next++
		}
		if next < len(earlier) && earlier[next].Start < end {
			continue
		}
		sum := sha256.Sum256([]byte(text[start:end]))
		matches = append(matches, SecretMatch{Rule: rule.name, Start: start, End: end, Digest: hex.EncodeToString(sum[:])})
	}
	return matches
}

// mergeMatches joins two lists that are each sorted by Start into one.
func mergeMatches(a, b []SecretMatch) []SecretMatch {
	if len(b) == 0 {
		return a
	}
	merged := make([]SecretMatch, 0, len(a)+len(b))
	for len(a) > 0 && len(b) > 0 {
		if a[0].Start < b[0].Start {
			merged, a = append(merged, a[0]), a[1:]
		} else {
			merged, b = append(merged, b[0]), b[1:]
		}
	}
	return append(append(merged, a...), b...)
}
