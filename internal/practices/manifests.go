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
	table := ""
	// depth counts the brackets still open in the [project] dependencies array, while one is being read.
	depth := 0
	for raw := range strings.SplitSeq(text, "\n") {
		if depth > 0 {
			quoted, open := scanArrayLine(raw, depth)
			if quoted {
				return true
			}
			depth = open
			continue
		}
		line := strings.TrimSpace(stripTOMLComment(raw))
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
			quoted, open := scanArrayLine(value, 0)
			if quoted {
				return true
			}
			depth = open
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

// scanArrayLine reads one line of a TOML array, once, from the depth of brackets open before it. It reports
// whether a string starts, which makes the array non-empty, and the depth open after the line. A comment ends the
// line, and the array ends where its brackets close.
func scanArrayLine(line string, depth int) (quoted bool, open int) {
	for _, r := range line {
		switch r {
		case '"', '\'':
			return true, depth
		case '#':
			return false, depth
		case '[':
			depth++
		case ']':
			depth--
			if depth <= 0 {
				return false, 0
			}
		}
	}
	return false, depth
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
