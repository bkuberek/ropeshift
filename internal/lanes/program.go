package lanes

// reasonProgram is what a session held to one program is told of any other shell command.
func reasonProgram(program string) string {
	return "ropeshift policy: this session runs only `" + program + " ...` in a shell, alone: no other program, no second command, " +
		"no substitution or variable, and no redirect to or from a file. Run the " + program + " command by itself."
}

// runsOnly says a command line runs the one program and nothing else: one simple command whose first word
// is the program's name, with no assignment or wrapper before it, no substitution or variable in any word,
// and no redirect but one that joins a descriptor to another. Anything else reads as another command,
// because this hook fails closed.
func runsOnly(text, program string) bool {
	commands, nested := parseShell(text)
	if len(nested) != 0 || len(commands) != 1 {
		return false
	}
	command := commands[0]
	if len(command.writes) != 0 || len(command.words) == 0 || command.words[0].text != program {
		return false
	}
	for _, word := range command.words {
		if word.dynamic {
			return false
		}
	}
	for _, read := range command.reads {
		if !isNumber(read.text) && read.text != "-" {
			return false
		}
	}
	return true
}
