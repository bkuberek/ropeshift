package lanes

import "strings"

// Decide returns the reason of the first rule that denies a call.
func (p DenyPolicy) Decide(tool, command string) (string, bool) {
	for _, rule := range p.Deny {
		if rule.Tool == tool && strings.HasPrefix(command, rule.CommandPrefix) {
			return rule.Reason, true
		}
	}
	return "", false
}
