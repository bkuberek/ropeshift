package ledger

import (
	"regexp"
	"strings"
)

// CIRedactionRulesVersion is the version of the rule set below. It changes with any rule that is added, removed, or
// changed, so a record of what was scanned can say by which rules. The rule set sits here, and nowhere else, so the
// scan of a repository's files for secrets (FR-PRAC-4) can adopt it.
const CIRedactionRulesVersion = 1

// redactionRule scans for one kind of credential. A rule with no group replaces the whole of a match with With, which
// is a template of regexp.ReplaceAllString. A rule with a group replaces only that group with the rule's marker and
// keeps the words around it, so a line still says which variable held the value. Skip says a value is no credential: one
// the forge already masked as ***, or one too short to be one.
type redactionRule struct {
	name    string
	pattern *regexp.Regexp
	with    string
	group   int
	skip    func(value string) bool
}

func (r redactionRule) marker() string { return "[redacted: " + r.name + "]" }

func (r redactionRule) apply(text string) string {
	if r.group == 0 {
		return r.pattern.ReplaceAllString(text, r.with)
	}
	var out strings.Builder
	last := 0
	for _, found := range r.pattern.FindAllStringSubmatchIndex(text, -1) {
		start, end := found[2*r.group], found[2*r.group+1]
		if start < 0 || (r.skip != nil && r.skip(text[start:end])) {
			continue
		}
		out.WriteString(text[last:start])
		out.WriteString(r.marker())
		last = end
	}
	out.WriteString(text[last:])
	return out.String()
}

// unlikelySecret says a value of an assignment is no secret: it is masked, it is the marker of a rule that already
// replaced it, or it is too short to hold one.
func unlikelySecret(value string) bool {
	return len(value) < 4 || strings.Trim(value, "*") == "" || strings.HasPrefix(value, "[redacted")
}

// whole is a rule that replaces everything it matches with its own marker.
func whole(name, pattern string) redactionRule {
	return redactionRule{name: name, pattern: regexp.MustCompile(pattern), with: "[redacted: " + name + "]"}
}

// credentialRules is the rule set, in the order it is applied: a private key goes first, because it holds lines that
// other rules would match inside it. A value the forge already masked as *** matches none of them and stays masked.
var credentialRules = []redactionRule{
	whole("private_key", `(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`),
	// The end of a key whose start was cut off by the window a log was read through: everything from the start of the
	// text to its END line goes, because nothing says where the body began.
	whole("private_key", `(?s)\A.*?-----END [A-Z ]*PRIVATE KEY-----`),
	whole("github_pat", `\bgithub_pat_[A-Za-z0-9_]{20,}`),
	whole("github_token", `\bgh[pousr]_[A-Za-z0-9]{20,}`),
	whole("gitlab_token", `\bglpat-[A-Za-z0-9_-]{20,}`),
	whole("npm_token", `\bnpm_[A-Za-z0-9]{36}\b`),
	whole("aws_access_key", `\b(?:AKIA|ASIA|AGPA|AIDA|AROA|ANPA|ANVA)[A-Z0-9]{16}\b`),
	{
		name:    "aws_secret_key",
		pattern: regexp.MustCompile(`(?i)(aws[_-]?secret[_-]?(?:access[_-]?)?key["']?\s*[:=]\s*["']?)([A-Za-z0-9/+=]{40})\b`),
		group:   2,
	},
	whole("stripe_key", `\b[sr]k_live_[A-Za-z0-9]{10,}`),
	whole("google_api_key", `\bAIza[0-9A-Za-z_-]{35}`),
	whole("slack_token", `\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	whole("api_key", `\bsk-(?:ant-)?[A-Za-z0-9_-]{20,}`),
	whole("jwt", `\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	whole("bearer_token", `(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	{name: "url_credentials", pattern: regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`), with: "${1}[redacted: url_credentials]@"},
	{
		name: "secret_assignment",
		pattern: regexp.MustCompile(
			`(?i)\b((?:[a-z0-9]+[_-])*(?:token|secret|password|passwd|pwd|api[_-]?key|access[_-]?key|private[_-]?key)["']?\s*[:=]\s*["']?)([^\s"']+)`),
		group: 2,
		skip:  unlikelySecret,
	},
}

// RedactCredentials replaces each match of a credential's shape in text with the name of the rule that found it.
func RedactCredentials(text string) string {
	for _, rule := range credentialRules {
		text = rule.apply(text)
	}
	return text
}

// RedactionRuleNames lists the rules of the set, in the order they are applied.
func RedactionRuleNames() []string {
	names := make([]string, 0, len(credentialRules))
	for _, rule := range credentialRules {
		names = append(names, rule.name)
	}
	return names
}
