package practices

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// goHasRequire says a go.mod holds a require directive, as a line or as a block.
func goHasRequire(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "require")
		if ok && rest != "" && (rest[0] == ' ' || rest[0] == '\t' || rest[0] == '(') {
			return true
		}
	}
	return false
}

// jsonHasEntries says a JSON object holds at least one of the keys, each an object or array that is not empty.
func jsonHasEntries(text string, keys ...string) (bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return false, err
	}
	for _, key := range keys {
		raw, ok := doc[key]
		if !ok {
			continue
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err == nil {
			if len(entries) > 0 {
				return true, nil
			}
			continue
		}
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// pyprojectHasDependencies says a pyproject.toml declares a dependency under [project] dependencies or
// [tool.poetry.dependencies], poetry's python entry not counting.
func pyprojectHasDependencies(text string) bool {
	lines := strings.Split(text, "\n")
	table := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(stripTOMLComment(lines[i]))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			table = strings.TrimSpace(strings.Trim(line, "[]"))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		switch table {
		case "project":
			if key != "dependencies" {
				continue
			}
			array := strings.TrimSpace(value)
			for bracketDepth(array) > 0 && i+1 < len(lines) {
				i++
				array += "\n" + stripTOMLComment(lines[i])
			}
			if strings.ContainsAny(array, `"'`) {
				return true
			}
		case "tool.poetry.dependencies":
			if key != "python" {
				return true
			}
		}
	}
	return false
}

// stripTOMLComment cuts a line at a # outside a string.
func stripTOMLComment(line string) string {
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return line[:i]
		}
	}
	return line
}

// bracketDepth counts the brackets of a TOML value that are still open, not counting any in a string.
func bracketDepth(value string) int {
	depth := 0
	var quote rune
	for _, r := range value {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '[':
			depth++
		case r == ']':
			depth--
		}
	}
	return depth
}

var pinnedRequirement = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\s*(\[[^\]]*\])?\s*==\s*[^=\s]`)

// requirementsPinned says every requirement line of a requirements.txt pins a version with ==. A comment, a blank
// line, and an option line, such as -r or --hash, is no requirement.
func requirementsPinned(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\\"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if !pinnedRequirement.MatchString(line) {
			return false
		}
	}
	return true
}

// makefileHasTarget says a Makefile has a rule for the target: a line that names it before a colon, and no
// assignment, as `hooks :=` is, and no declaration, as `.PHONY: hooks` is.
func makefileHasTarget(text, target string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" || line[0] == '\t' || line[0] == '#' || line[0] == ' ' {
			continue
		}
		targets, after, ok := strings.Cut(line, ":")
		if !ok || strings.ContainsAny(targets, "=$") {
			continue
		}
		after = strings.TrimPrefix(after, ":")
		if strings.HasPrefix(after, "=") {
			continue
		}
		if slices.Contains(strings.Fields(targets), target) {
			return true
		}
	}
	return false
}
