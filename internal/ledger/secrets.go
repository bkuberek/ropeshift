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
	var found []SecretMatch
	for _, rule := range credentialRules {
		if !rule.secret {
			continue
		}
		for _, loc := range rule.pattern.FindAllStringSubmatchIndex(text, -1) {
			start, end := loc[0], loc[1]
			if rule.group != 0 {
				start, end = loc[2*rule.group], loc[2*rule.group+1]
			}
			if start < 0 || (rule.skip != nil && rule.skip(text[start:end])) || overlaps(found, start, end) {
				continue
			}
			sum := sha256.Sum256([]byte(text[start:end]))
			found = append(found, SecretMatch{Rule: rule.name, Start: start, End: end, Digest: hex.EncodeToString(sum[:])})
		}
	}
	slices.SortFunc(found, func(a, b SecretMatch) int { return a.Start - b.Start })
	return found
}

func overlaps(found []SecretMatch, start, end int) bool {
	return slices.ContainsFunc(found, func(m SecretMatch) bool { return start < m.End && m.Start < end })
}
