package cli

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// program is the name this package's commands run under.
const program = "ropeshift"

// reference names the full command reference, which every list of commands points to.
const reference = "docs/cli.md describes every command and the exit codes: https://github.com/bkuberek/ropeshift/blob/main/docs/cli.md"

// widest is the widest left column a list aligns to. A longer usage puts its summary on the next line.
const widest = 50

// Command is what help says about one command.
type Command struct {
	// Name is the words a person types, such as "work create".
	Name string
	// Args is what follows the name in its usage line.
	Args string
	// Summary is one line on what the command does.
	Summary string
	// Detail is a paragraph only the command's own help prints.
	Detail string
	// Family, when set, is the line the list of every command prints for this one, so commands that
	// differ only by one name print as one line.
	Family string
}

// Usage is the command's usage line, after the program's name.
func (c Command) Usage() string {
	return strings.TrimSpace(c.Name + " " + c.Args)
}

// Group is the commands help prints under one heading.
type Group struct {
	Title    string
	Commands []Command
}

// AsksForHelp reports an argument that asks for help, in each spelling the flag package accepts.
func AsksForHelp(arg string) bool {
	switch arg {
	case "-h", "--h", "-help", "--help":
		return true
	}
	return false
}

// row is one line of a list: a usage and its summary.
type row struct {
	usage, summary string
}

// WriteList prints a program's usage: every group with one line for each command, how to ask about
// one command, and where the reference is.
func WriteList(w io.Writer, name string, groups []Group) {
	sections := make([][]row, len(groups))
	var all []row
	for i, group := range groups {
		for _, command := range group.Commands {
			line := row{cmp.Or(command.Family, command.Usage()), command.Summary}
			if n := len(sections[i]); n > 0 && sections[i][n-1] == line {
				continue
			}
			sections[i] = append(sections[i], line)
			all = append(all, line)
		}
	}
	width := columnWidth(all)
	fmt.Fprintf(w, "usage: %s <command> [arguments]\n", name)
	for i, group := range groups {
		fmt.Fprintf(w, "\n%s:\n", group.Title)
		writeRows(w, sections[i], width)
	}
	fmt.Fprintf(w, "\nRun '%s help <command>' or '%s <command> --help' for one command's usage and flags.\n%s\n", name, name, reference)
}

// WriteUsage prints one command's help: its usage line, what it does, and its flags when set holds any.
func WriteUsage(w io.Writer, name string, command Command, set *flag.FlagSet) {
	fmt.Fprintf(w, "usage: %s %s\n", name, command.Usage())
	if command.Summary != "" {
		fmt.Fprintf(w, "\n%s\n", sentence(command.Summary))
	}
	if command.Detail != "" {
		fmt.Fprintf(w, "\n%s\n", command.Detail)
	}
	if set == nil {
		return
	}
	hasFlags := false
	set.VisitAll(func(*flag.Flag) { hasFlags = true })
	if !hasFlags {
		return
	}
	fmt.Fprint(w, "\nFlags:\n")
	out := set.Output()
	set.SetOutput(w)
	set.PrintDefaults()
	set.SetOutput(out)
}

// writeGroup prints the commands under one first word.
func writeGroup(w io.Writer, word string, commands []Command) {
	rows := make([]row, 0, len(commands))
	for _, command := range commands {
		rows = append(rows, row{command.Usage(), command.Summary})
	}
	fmt.Fprintf(w, "usage: %s %s <command> [arguments]\n\n", program, word)
	writeRows(w, rows, columnWidth(rows))
	fmt.Fprintf(w, "\nRun '%s help %s <command>' for one command's usage and flags.\n", program, word)
}

// columnWidth is the widest usage that fits the column.
func columnWidth(rows []row) int {
	width := 0
	for _, r := range rows {
		if n := len(r.usage); n <= widest && n > width {
			width = n
		}
	}
	return width
}

func writeRows(w io.Writer, rows []row, width int) {
	for _, r := range rows {
		if len(r.usage) > width {
			fmt.Fprintf(w, "  %s\n  %*s  %s\n", r.usage, width, "", r.summary)
			continue
		}
		fmt.Fprintf(w, "  %-*s  %s\n", width, r.usage, r.summary)
	}
}

// sentence makes a summary a sentence: a capital first, and a full stop last.
func sentence(summary string) string {
	first, size := utf8.DecodeRuneInString(summary)
	text := string(unicode.ToUpper(first)) + summary[size:]
	if strings.HasSuffix(text, ".") {
		return text
	}
	return text + "."
}
