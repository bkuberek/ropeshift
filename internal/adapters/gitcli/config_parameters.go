package gitcli

import "strings"

// configParameters is the variable `git -c` sets for the commands it starts. Git reads it after the count variables,
// so a setting it holds beats the same setting there, and beats one a command adds through them.
const configParameters = "GIT_CONFIG_PARAMETERS"

// foldParameters replaces GIT_CONFIG_PARAMETERS in a command's environment with the same settings in git's count
// variables, after any the environment already holds, which is the order git reads the two in. The settings the
// environment holds stay, and a setting added after them beats them. A value that is not in the format git writes is
// dropped with the variable.
func foldParameters(env []string) []string {
	text, found := "", false
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, configParameters+"="); ok {
			text, found = value, true
			continue
		}
		kept = append(kept, entry)
	}
	if !found {
		return env
	}
	settings, _ := parseParameters(text)
	for _, setting := range settings {
		kept = withGitConfig(kept, setting[0], setting[1])
	}
	return kept
}

// parseParameters reads the settings of a GIT_CONFIG_PARAMETERS value: shell-quoted entries separated by spaces,
// each `'key'='value'`, with `'key'=` for a key that has no value, which reads as true, and `'key=value'` as git
// wrote it before 2.31. It reports false for a value that is in neither format.
func parseParameters(text string) ([][2]string, bool) {
	var settings [][2]string
	for rest := strings.TrimLeft(text, " "); rest != ""; rest = strings.TrimLeft(rest, " ") {
		word, after, ok := unquote(rest)
		if !ok {
			return nil, false
		}
		rest = after
		key, value := word, "true"
		switch {
		case strings.HasPrefix(rest, "="):
			rest = rest[1:]
			if rest != "" && rest[0] != ' ' {
				if value, rest, ok = unquote(rest); !ok {
					return nil, false
				}
			}
		case rest == "" || rest[0] == ' ':
			if before, after, found := strings.Cut(word, "="); found {
				key, value = before, after
			}
		default:
			return nil, false
		}
		settings = append(settings, [2]string{key, value})
	}
	return settings, true
}

// unquote reads one word the way git quotes it for a shell: single-quoted runs, and a backslash before a character
// that stands for the character, as in `'it'\”s'`. It returns the word and what follows it.
func unquote(text string) (string, string, bool) {
	var word strings.Builder
	read := false
	for text != "" {
		switch text[0] {
		case '\'':
			end := strings.IndexByte(text[1:], '\'')
			if end < 0 {
				return "", "", false
			}
			word.WriteString(text[1 : 1+end])
			text = text[end+2:]
		case '\\':
			if len(text) < 2 {
				return "", "", false
			}
			word.WriteByte(text[1])
			text = text[2:]
		default:
			return word.String(), text, read
		}
		read = true
	}
	return word.String(), "", read
}
