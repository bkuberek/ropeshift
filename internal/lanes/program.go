package lanes

import (
	"slices"
	"strings"
)

// CommandWord is a program's path as a shell command names it: as it stands, or quoted when it holds a
// character a shell would read, such as the space in a macOS state directory. A session's rules and its
// role name the program this way, and its hook reads the word back to the path.
func CommandWord(path string) string {
	for _, c := range path {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/._+-", c)) {
			return ShellQuote(path)
		}
	}
	return path
}

// reasonProgram is what a session held to one program is told of any other shell command.
func reasonProgram(program string) string {
	return "ropeshift policy: this session runs only `" + program + " ...` in a shell, alone: no other program, no second command, " +
		"no substitution, variable, glob, or here-document, and no redirect to or from a file, and only one of its commands. Run the " + program + " command by itself."
}

// runsOnly says a command line runs the one program and nothing else: one simple command whose first word
// is the program's name, with no assignment or wrapper before it, no substitution, variable, or unquoted
// `~`, `*`, `?`, `[`, or `{` in any word, no here-document or here-string, and no redirect but one that
// joins a descriptor to another. With commands, the words after the program name one of them, and no word
// is one of the refused flags in any form. Anything else reads as another command, because this hook fails
// closed.
func runsOnly(text, program string, commands, refused []string) bool {
	parsed, nested := parseShell(text)
	if len(nested) != 0 || len(parsed) != 1 {
		return false
	}
	command := parsed[0]
	if command.heredoc || len(command.writes) != 0 || len(command.words) == 0 || command.words[0].text != program {
		return false
	}
	for _, word := range command.words {
		if word.dynamic || word.glob || refusedFlag(word.text, refused) {
			return false
		}
	}
	for _, read := range command.reads {
		if !isNumber(read.text) && read.text != "-" {
			return false
		}
	}
	return len(commands) == 0 || namesCommand(command.words[1:], commands)
}

// refusedFlag says a word is one of the refused flags: `-name`, `--name`, `-name=value`, or `--name=value`.
func refusedFlag(word string, refused []string) bool {
	for _, name := range refused {
		for _, form := range []string{"-" + name, "--" + name} {
			if word == form || strings.HasPrefix(word, form+"=") {
				return true
			}
		}
	}
	return false
}

// maxCommandWords is the longest name a command has.
const maxCommandWords = 3

// namesCommand says the words begin with the name of one of the commands.
func namesCommand(words []shellWord, commands []string) bool {
	for n := min(len(words), maxCommandWords); n > 0; n-- {
		name := make([]string, 0, n)
		for _, word := range words[:n] {
			name = append(name, word.text)
		}
		if slices.Contains(commands, strings.Join(name, " ")) {
			return true
		}
	}
	return false
}
