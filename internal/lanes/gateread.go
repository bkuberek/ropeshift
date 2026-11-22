package lanes

import (
	"path"
	"slices"
	"strings"
)

// maxGateWords bounds the words of one command, each of which may be read as a place a command starts. Past it, a
// command is not understood.
const maxGateWords = 4096

// maxGateBytes bounds the command read for pushes and gates, maxGateRead the bytes read of it with what nests in it,
// and maxGateWork the bytes gone over again from each place a command could start. Past any, a command is not
// understood, so it asks, and reading it costs the hook and the worker little. A long command of many short ones
// goes over little again, however many it holds.
const (
	maxGateBytes = 32 << 10
	maxGateRead  = 8 * maxGateBytes
	maxGateWork  = 16 * maxGateBytes
)

// shellGates is what one reading of a command found: each push it could take apart, whether it pushes, whether a gate
// names one of its commands, and whether it holds what could be either and cannot be read.
//
// It follows a command as far as its text says: through wrappers, shells, substitutions, here-documents, and the
// words a program may run. It does not open a script, resolve an alias of a person's shell, or know what a program
// does by itself.
type shellGates struct {
	gates  []gatePattern
	pushes []shellPush
	pushed bool
	gated  bool
	unread bool
	// mayPush says git runs a subcommand only a shell knows.
	mayPush bool
	// read counts the bytes read, and work those gone over again from each place a command could start.
	read int
	work int
}

// shellPush is one push's arguments, and whether the shell runs it, which a word some program may run as a command
// does not say.
type shellPush struct {
	args []shellWord
	runs bool
}

// gatePattern is a gate of shell commands: its prefix, and the words of it.
type gatePattern struct {
	prefix string
	fields []string
}

// shellPlace is where a command stands.
type shellPlace struct {
	command shellCommand
	depth   int
	// loose says the text is a word some program may run as a command, such as `ssh host '...'`. Only a push or a
	// gate that starts a command counts there, because such a word is as often prose or a pattern.
	loose bool
	// input says the command takes more words from where this cannot read, as under `xargs` and `find -exec`, and
	// replace is what stands for those words.
	input   bool
	replace string
	// varying says the text is of a word that expands, whose substitutions left nothing in it to read, or of one that
	// words this cannot read may fill in.
	varying bool
}

func (at shellPlace) deeper() shellPlace {
	at.depth++
	return at
}

// readShellGates reads a shell command for pushes and for the commands gates name.
func readShellGates(command string, gates []GateRule) *shellGates {
	read := &shellGates{}
	if len(command) > maxGateBytes {
		read.unread = true
		return read
	}
	for _, gate := range gates {
		if gate.Tool != ToolBash {
			continue
		}
		pattern := gatePattern{prefix: gate.CommandPrefix}
		for _, field := range strings.Fields(gate.CommandPrefix) {
			pattern.fields = append(pattern.fields, strings.Trim(field, `'"`))
		}
		read.gates = append(read.gates, pattern)
	}
	read.script(command, shellPlace{})
	return read
}

// spend counts one more reading of words, and says false once maxGateWork is spent.
func (g *shellGates) spend(words []shellWord) bool {
	for _, word := range words {
		if g.work > maxGateWork {
			break
		}
		g.work += len(word.text) + 1
	}
	if g.work > maxGateWork {
		g.unread = true
		return false
	}
	return true
}

func (g *shellGates) unsure(at shellPlace) {
	if !at.loose {
		g.unread = true
	}
}

// script reads a text a shell runs.
func (g *shellGates) script(text string, at shellPlace) {
	g.read += len(text)
	if at.depth > maxShellDepth || g.read > maxGateRead {
		g.unread = true
		return
	}
	parsed := parseScript(text)
	if parsed.unread {
		g.unsure(at)
	}
	inside := shellPlace{depth: at.depth + 1, loose: at.loose, varying: at.varying}
	for _, inner := range parsed.nested {
		g.script(inner, inside)
	}
	for _, command := range parsed.commands {
		g.command(shellPlace{command: command, depth: at.depth, loose: at.loose, replace: at.replace, varying: at.varying})
	}
}

func (g *shellGates) command(at shellPlace) {
	words := at.command.words
	if len(words) == 0 {
		return
	}
	if len(words) > maxGateWords {
		g.unread = true
		return
	}
	g.matchLine(words)
	for {
		words = words[min(commandStart(words), len(words)):]
		if len(words) == 0 {
			return
		}
		if name, _, found := strings.Cut(words[0].text, "="); found && assignedName(name) {
			words = words[1:]
			continue
		}
		// zsh runs a command that follows a test or arithmetic, as in `if [[ ... ]] git push`.
		if words[0].dynamic && words[0].text == "((" {
			words = words[1:]
			continue
		}
		if !isKeyword(words[0], "[[") {
			break
		}
		end := slices.IndexFunc(words, func(word shellWord) bool { return isKeyword(word, "]]") })
		if end < 0 {
			return
		}
		words = words[end+1:]
	}
	// What follows these are words, and the commands of their bodies stand apart.
	if isKeyword(words[0], "for", "select", "case") {
		return
	}
	g.run(words, at)
}

// run reads a simple command from its program on.
func (g *shellGates) run(words []shellWord, at shellPlace) {
	if at.depth > maxShellDepth {
		g.unread = true
		return
	}
	var name string
	for {
		if g.work > maxGateWork {
			g.unread = true
			return
		}
		if len(words) == 0 {
			if at.input {
				g.unsure(at)
			}
			return
		}
		var known bool
		if name, known = programName(words[0], at.replace); !known {
			g.unsure(at)
			g.search(words, at)
			return
		}
		if text := words[0].text; len(text) > 1 && strings.HasPrefix(text, "-") {
			// No program's name starts with a dash, so this misread what stands before the command.
			g.unsure(at)
		}
		g.matchLine(words)
		g.matchWords(name, words[1:], at, true)
		wrapper, wraps := gateWrappers[name]
		if !wraps {
			break
		}
		var ended bool
		if words, at, ended = g.unwrap(name, wrapper, words[1:], at); ended {
			return
		}
	}
	program, args := words[0], words[1:]
	switch {
	case name == "git":
		g.git(args, at, true)
		return
	case slices.Contains(pushPrograms, name):
		g.push(args, at)
		return
	case name == "eval":
		expands := slices.ContainsFunc(args, func(word shellWord) bool { return word.expands })
		if expands || (at.input && len(args) == 0) {
			g.unsure(at)
		}
		inner := at.deeper()
		inner.varying = inner.varying || expands
		g.script(joinWords(args), inner)
		return
	case slices.Contains(gateShells, name):
		g.shell(args, at)
		g.search(args, at)
		return
	case program.text == "." || name == "source":
		if len(args) == 0 || readsStandardInput(args[0].text) {
			g.fedScript(at)
		} else if _, known := programName(args[0], at.replace); !known {
			g.unsure(at)
		}
		return
	case name == "find":
		g.findCommands(args, at)
		return
	case name == "alias":
		g.alias(args, at)
		return
	case name == "gh":
		if len(args) < 2 {
			return
		}
		if args[0].text == "alias" && args[1].text == "set" {
			g.unsure(at)
		}
		if args[0].text == "repo" && args[1].text == "create" && slices.ContainsFunc(args[2:], func(word shellWord) bool { return word.text == "--push" }) {
			g.push(nil, at)
		}
		return
	case name == "rg" && !slices.ContainsFunc(args, func(word shellWord) bool { return strings.HasPrefix(word.text, "--pre") }):
		return
	case slices.Contains(dataPrograms, name):
		return
	}
	if kind, runs := interpreterOf(name); runs {
		g.code(kind, args, at, true)
		g.search(args, at)
		return
	}
	if slices.Contains(textRunners, name) {
		g.runsText(name, args, at)
	}
	searched := at
	if name == "parallel" {
		// parallel builds its command from words this cannot read, as xargs does.
		searched.input, searched.replace = true, "{}"
	}
	g.search(args, searched)
	for _, body := range at.command.fed {
		g.script(body.text, shellPlace{depth: at.depth + 1, loose: true, varying: at.varying || body.expands})
	}
}

// gateWrapper is how a program that runs the rest of its line takes its own words.
type gateWrapper struct {
	// valued are its options that take the next word, and text those of them whose value is a command's text.
	valued []string
	text   []string
	// operands stand before the command, and assigns says `NAME=value` may.
	operands int
	assigns  bool
}

// gateWrappers run the command that follows them.
var gateWrappers = map[string]gateWrapper{
	"env": {
		valued: []string{"-u", "--unset", "-C", "--chdir", "-P", "-a", "--argv0", "-S", "--split-string"},
		text:   []string{"-S", "--split-string"}, assigns: true,
	},
	"timeout":    {valued: []string{"-s", "--signal", "-k", "--kill-after"}, operands: 1},
	"nice":       {valued: []string{"-n", "--adjustment"}},
	"ionice":     {valued: strings.Fields("-c --class -n --classdata -p --pid")},
	"time":       {valued: []string{"-f", "--format", "-o", "--output"}},
	"stdbuf":     {valued: []string{"-i", "--input", "-o", "--output", "-e", "--error"}},
	"exec":       {valued: []string{"-a"}},
	"flock":      {valued: strings.Fields("-w --timeout -E -c --command"), text: strings.Fields("-c --command"), operands: 1},
	"caffeinate": {valued: []string{"-t", "-w"}},
	"sudo": {
		valued: []string{
			"-u", "--user", "-g", "--group", "-C", "-D", "--chdir", "-h", "--host", "-p", "--prompt", "-r", "-t", "-T", "-U", "-R", "--chroot",
		},
		assigns: true,
	},
	"doas": {valued: []string{"-u", "-C"}},
	"xargs": {valued: []string{
		"-I", "-L", "-n", "--max-args", "-P", "--max-procs", "-s", "--max-chars", "-d", "--delimiter", "-a", "--arg-file", "-E", "-J", "-R",
	}},
	// zsh's `repeat`, and its `-` before a command.
	"repeat": {operands: 1}, "-": {},
	"nohup": {}, "setsid": {}, "command": {}, "builtin": {}, "noglob": {}, "nocorrect": {}, "busybox": {},
}

// unwrap takes a wrapper's own words away. Ended says nothing of the command is left to read.
func (g *shellGates) unwrap(name string, wrapper gateWrapper, args []shellWord, at shellPlace) ([]shellWord, shellPlace, bool) {
	if !g.spend(args) {
		return nil, at, true
	}
	operands, options, split := wrapper.operands, true, false
	for len(args) != 0 {
		word := args[0]
		option := options && strings.HasPrefix(word.text, "-")
		assigns := wrapper.assigns && strings.Contains(word.text, "=")
		if !option && !assigns && operands == 0 {
			break
		}
		// A word such as `=git` could be the command: env takes it as an assignment, and zsh as git's path. An
		// expansion in an option's value, an assignment, or an operand reads as the one word it is written as, unless
		// it is written to give many. An option's name is never one.
		if (assigns && !option && !isAssignment(word.text)) || word.manyWords() ||
			(option && word.expands && !namesOption(word, wrapper.valued)) {
			g.unsure(at)
		}
		split = split || word.split
		args = args[1:]
		switch {
		case option && word.text == "--":
			options = false
		case option:
			flag, value, takes := readOption(word.text, wrapper.valued)
			held := word
			// An expansion against an option is its value, though a substitution leaves no text of it.
			if takes && !word.expands {
				if len(args) == 0 {
					return nil, at, !at.input && name != "xargs"
				}
				held, args = args[0], args[1:]
				value = held.text
				split = split || held.split
				if held.manyWords() {
					g.unsure(at)
				}
			}
			switch {
			case name == "command" && strings.ContainsAny(flag, "vV"):
				// `command -v` only says where a program is.
				return nil, at, true
			case slices.Contains(wrapper.text, flag):
				if held.expands || (name == "env" && slices.ContainsFunc(args, func(word shellWord) bool { return word.expands })) {
					g.unsure(at)
				}
				if name == "env" {
					value += " " + joinWords(args)
				}
				g.script(value, at.deeper())
				return nil, at, true
			case name != "xargs":
			case flag == "-I" || flag == "-J":
				// With a placeholder only a shell knows, any word could be one.
				if held.expands {
					g.unsure(at)
				}
				at.replace = value
			case flag == "--replace" || strings.HasPrefix(flag, "-i"):
				if held.expands {
					g.unsure(at)
				}
				if at.replace = value; value == "" {
					at.replace = "{}"
				}
			}
		case assigns:
		default:
			operands--
		}
	}
	// After an expansion a shell splits, no command is left to read where nothing follows, or where what follows reads
	// as an operand, which says the expansion held options, or as what only follows git.
	if split && (len(args) == 0 || operand.MatchString(args[0].text) || slices.Contains(pushSubcommands, args[0].text)) {
		g.unsure(at)
	}
	if name == "xargs" {
		// With no command, xargs runs echo.
		if len(args) == 0 {
			return nil, at, true
		}
		at.input = true
	}
	return args, at, false
}

// readOption takes one option apart: its name, the value joined to it, and whether its value is the next word.
func readOption(text string, valued []string) (flag, value string, takes bool) {
	if strings.HasPrefix(text, "--") {
		flag, value, joined := strings.Cut(text, "=")
		return flag, value, !joined && slices.Contains(valued, flag)
	}
	for i := 1; i < len(text); i++ {
		if flag := "-" + text[i:i+1]; slices.Contains(valued, flag) {
			return flag, text[i+1:], i+1 == len(text)
		}
	}
	return text, "", false
}

// namesOption says a word that expands writes its option's name out, with only the value left to a shell.
func namesOption(word shellWord, valued []string) bool {
	literal := word.literal()
	if strings.HasPrefix(literal, "--") {
		return strings.Contains(literal, "=")
	}
	flag, _, _ := readOption(literal, valued)
	return slices.Contains(valued, flag)
}

// programName reads the program a word names, by its last part, in lower case, as a file system that folds case
// finds it. Known is false where only a shell could say.
func programName(word shellWord, replace string) (string, bool) {
	text := word.text
	if replace != "" && strings.Contains(text, replace) {
		return "", false
	}
	if word.expands {
		if word.split {
			return "", false
		}
		text = text[min(word.tail, len(text)):]
		slash := strings.LastIndexByte(text, '/')
		if slash < 0 || slash+1 == len(text) {
			return "", false
		}
		text = text[slash+1:]
	}
	if word.glob && (braced(word.text) || patterned(path.Base(text))) {
		return "", false
	}
	// zsh reads `{` against a word as the start of a group.
	if word.glob && len(word.text) > 1 && strings.HasPrefix(word.text, "{") {
		return "", false
	}
	if text == "" {
		return "", true
	}
	// zsh reads `=git` as git's path.
	return strings.ToLower(path.Base(strings.TrimPrefix(text, "="))), true
}

func braced(text string) bool {
	return strings.Contains(text, "{") && strings.Contains(text, "}") && (strings.Contains(text, ",") || strings.Contains(text, ".."))
}

func patterned(text string) bool {
	return strings.ContainsAny(text, "*?") || (strings.Contains(text, "[") && strings.Contains(text, "]"))
}

// varies says a word's value is not its text.
func varies(word shellWord, replace string) bool {
	return word.expands || (word.glob && (braced(word.text) || patterned(word.text))) ||
		(replace != "" && strings.Contains(word.text, replace))
}

// pushPrograms are git's own programs that push, by the names they have on disk.
var pushPrograms = []string{"git-push", "git-send-pack", "git-http-push"}

// pushSubcommands push, and nestedPushes do with the word that follows.
var pushSubcommands = []string{"push", "send-pack", "http-push"}

var nestedPushes = map[string]string{"subtree": "push", "lfs": "push", "svn": "dcommit"}

// gitGlobalValues are git's options before a subcommand that take the next word when no `=` joins them.
var gitGlobalValues = strings.Fields("-C -c --git-dir --work-tree --namespace --super-prefix --config-env --attr-source")

// gitCommands are git's own subcommands, which no alias replaces ([git config]). Any other could be an alias of a
// push, or of a gated command. One that runs whatever git command it is given, such as for-each-repo, is none of them.
//
// [git config]: https://git-scm.com/docs/git-config#Documentation/git-config.txt-alias
var gitCommands = strings.Fields(`
	add am annotate apply archive bisect blame branch bugreport bundle cat-file check-attr check-ignore check-mailmap
	check-ref-format checkout checkout-index cherry cherry-pick clean clone column commit commit-graph commit-tree
	config count-objects credential describe diagnose diff diff-files diff-index diff-tree difftool fast-export
	fast-import fetch fetch-pack filter-branch fmt-merge-msg for-each-ref format-patch fsck gc get-tar-commit-id grep
	hash-object help hook index-pack init init-db interpret-trailers lfs log ls-files ls-remote ls-tree mailinfo
	mailsplit maintenance merge merge-base merge-file merge-index merge-tree mergetool mktag mktree multi-pack-index mv
	name-rev notes pack-objects pack-refs patch-id prune prune-packed pull range-diff read-tree rebase reflog remote
	repack replace request-pull rerere reset restore rev-list rev-parse revert rm shortlog show show-branch show-index
	show-ref sparse-checkout stage stash status stripspace submodule subtree switch symbolic-ref tag unpack-objects
	update-index update-ref var verify-commit verify-pack verify-tag version whatchanged worktree write-tree`)

// gitRunners run commands their arguments name, and gitTextOptions are options whose value a shell runs.
var (
	gitRunners     = []string{"rebase", "bisect", "submodule", "filter-branch", "difftool"}
	gitTextOptions = []string{"--exec", "--extcmd", "--upload-pack", "--receive-pack", "--tree-filter"}
)

// git reads one git command after its own options. Starts says git is the command, and not a word some program may
// run as one, where only a push, a gate, or a subcommand only a shell knows counts.
func (g *shellGates) git(args []shellWord, at shellPlace, starts bool) {
	if !g.spend(args) {
		return
	}
	split := false
	for len(args) != 0 && strings.HasPrefix(args[0].text, "-") {
		option := args[0]
		args = args[1:]
		if option.glob && braced(option.text) {
			g.unsure(at)
		}
		// Only a long option's value can be a shell's to know, after its `=`.
		if option.expands && (option.manyWords() || !strings.HasPrefix(option.literal(), "--") || !strings.Contains(option.literal(), "=")) {
			g.unsure(at)
		}
		split = split || option.split
		flag, value, joined := strings.Cut(option.text, "=")
		if !joined && slices.Contains(gitGlobalValues, flag) && len(args) != 0 {
			split = split || args[0].split
			if args[0].manyWords() {
				g.unsure(at)
			}
			value, args = args[0].text, args[1:]
		}
		if flag != "-c" {
			continue
		}
		// A setting such as `core.pager` can name a command.
		if _, setting, found := strings.Cut(value, "="); found && strings.ContainsAny(setting, shellSpecial) {
			g.script(strings.TrimPrefix(setting, "!"), shellPlace{depth: at.depth + 1, loose: true})
		}
	}
	if len(args) == 0 {
		if at.input || split {
			g.unsure(at)
		}
		g.mayPush = g.mayPush || (at.input && !at.loose)
		if at.loose && (at.input || at.varying || split) {
			g.gateGit()
		}
		return
	}
	if varies(args[0], at.replace) {
		g.unsure(at)
		g.mayPush = g.mayPush || !at.loose
		if at.loose {
			g.gateGit()
		}
		return
	}
	g.matchGit(args, at)
	subcommand := args[0].text
	rest := args[1:]
	if slices.Contains(pushSubcommands, subcommand) {
		g.push(rest, at)
		return
	}
	if verb, nests := nestedPushes[subcommand]; nests {
		for i, word := range rest {
			if word.text == verb {
				g.push(rest[i+1:], at)
				return
			}
		}
		if starts && ((at.input && len(rest) == 0) || (len(rest) != 0 && varies(rest[0], at.replace))) {
			g.unsure(at)
		}
	}
	if !starts {
		return
	}
	if !slices.Contains(gitCommands, subcommand) {
		g.unsure(at)
		return
	}
	g.gitRuns(subcommand, rest, at)
}

// gateGit holds git with a subcommand only a shell knows as gated where a gate is configured for git. Outside loose
// text such a command asks anyway, as one this cannot read.
func (g *shellGates) gateGit() {
	g.gated = g.gated || slices.ContainsFunc(g.gates, func(gate gatePattern) bool {
		return len(gate.fields) != 0 && strings.EqualFold(path.Base(gate.fields[0]), "git")
	})
}

// gitRuns reads the commands git runs for a subcommand.
func (g *shellGates) gitRuns(subcommand string, args []shellWord, at shellPlace) {
	runner := slices.Contains(gitRunners, subcommand)
	if at.input && runner {
		g.unsure(at)
	}
	verb := map[string]string{"bisect": "run", "submodule": "foreach"}[subcommand]
	for i, word := range args {
		if verb != "" && word.text == verb {
			rest := args[i+1:]
			for len(rest) != 0 && strings.HasPrefix(rest[0].text, "-") {
				rest = rest[1:]
			}
			if slices.ContainsFunc(rest, func(word shellWord) bool { return word.expands }) {
				g.unsure(at)
			}
			// git gives a single word to a shell as it stands, and runs more than one as a command.
			g.script(joinWords(rest), at.deeper())
			if len(rest) > 1 {
				g.run(rest, at.deeper())
			}
			return
		}
		flag, value, joined := strings.Cut(word.text, "=")
		runs := slices.Contains(gitTextOptions, flag)
		if runner && !runs && strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") {
			// `-x` can close a cluster, or lead its value.
			if cut := strings.IndexByte(word.text, 'x'); cut > 0 {
				runs, value, joined = true, word.text[cut+1:], cut+1 < len(word.text)
			}
		}
		held := word
		switch {
		case !runs && subcommand == "filter-branch" && strings.ContainsAny(word.text, shellSpecial):
			value = word.text
		case !runs:
			continue
		case !joined && i+1 < len(args):
			held, value = args[i+1], args[i+1].text
		}
		if held.expands || word.expands {
			g.unsure(at)
		}
		g.script(value, at.deeper())
	}
}

func (g *shellGates) push(args []shellWord, at shellPlace) {
	g.spend(args)
	g.pushed = true
	g.pushes = append(g.pushes, shellPush{args: args, runs: !at.loose})
}

// gateShells take a command as their text, or from their standard input.
var gateShells = append(slices.Clone(shells), "ash", "mksh", "rbash", "fish", "csh", "tcsh")

// shell reads a shell's options for the text or the script it runs.
func (g *shellGates) shell(args []shellWord, at shellPlace) {
	if !g.spend(args) {
		return
	}
	coded, standard := false, false
	for len(args) != 0 {
		word := args[0]
		text := word.text
		if text == "-" || text == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "+") {
			break
		}
		args = args[1:]
		if word.expands {
			g.unsure(at)
		}
		if strings.HasPrefix(text, "--") {
			switch text {
			case "--version", "--help":
				return
			case "--command":
				coded = true
			case "--rcfile", "--init-file":
				if len(args) != 0 {
					args = args[1:]
				}
			}
			continue
		}
		for _, letter := range text[1:] {
			switch letter {
			case 'c':
				coded = coded || text[0] == '-'
			case 's':
				standard = true
			case 'o', 'O':
				if len(args) != 0 {
					if args[0].manyWords() {
						g.unsure(at)
					}
					args = args[1:]
				}
			}
		}
	}
	switch {
	case coded && len(args) == 0:
		g.unsure(at)
		return
	case coded:
		if args[0].expands || (at.replace != "" && strings.Contains(args[0].text, at.replace)) {
			g.unsure(at)
		}
		g.script(args[0].text, shellPlace{depth: at.depth + 1, loose: at.loose, varying: at.varying || args[0].expands})
	case standard || len(args) == 0 || readsStandardInput(args[0].text):
		g.fedScript(at)
	default:
		if _, known := programName(args[0], at.replace); !known {
			g.unsure(at)
		}
	}
}

func readsStandardInput(text string) bool {
	if text == "-" {
		return true
	}
	file := path.Clean(text)
	return file == "/dev/stdin" || strings.HasPrefix(file, "/dev/fd/") || strings.HasPrefix(file, "/proc/self/fd/")
}

// fedScript reads what feeds a shell its script. With only a pipe or a terminal to feed it, nothing can be read.
func (g *shellGates) fedScript(at shellPlace) {
	if len(at.command.fed) == 0 {
		g.unsure(at)
	}
	for _, body := range at.command.fed {
		if body.expands {
			g.unsure(at)
		}
		g.script(body.text, shellPlace{depth: at.depth + 1, loose: at.loose, varying: at.varying || body.expands})
	}
}

func (g *shellGates) findCommands(args []shellWord, at shellPlace) {
	for i := 0; i < len(args); i++ {
		if !slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, args[i].text) {
			continue
		}
		end := i + 1
		for end < len(args) && args[end].text != ";" && (args[end].text != "+" || args[end-1].text != "{}") {
			end++
		}
		if i+1 < end {
			inner := at.deeper()
			inner.input, inner.replace = true, "{}"
			g.run(args[i+1:end], inner)
		}
		i = end
	}
}

// alias refuses to follow an alias of git or of a gated program, whose later uses name neither.
func (g *shellGates) alias(args []shellWord, at shellPlace) {
	for _, word := range args {
		_, value, found := strings.Cut(word.text, "=")
		if !found {
			continue
		}
		if word.expands {
			g.unsure(at)
		}
		for _, name := range codeWords(value) {
			if strings.EqualFold(name, "git") {
				g.unsure(at)
			}
			for _, gate := range g.gates {
				if len(gate.fields) != 0 && path.Base(gate.fields[0]) == name {
					g.unsure(at)
				}
			}
		}
	}
}

// textRunners hand their words to a shell.
var textRunners = []string{"watch", "trap", "su", "parallel"}

func (g *shellGates) runsText(name string, args []shellWord, at shellPlace) {
	for _, word := range args {
		if word.expands {
			g.unsure(at)
		} else if strings.ContainsAny(word.text, shellSpecial) {
			g.script(word.text, at.deeper())
		}
	}
	// With no command, parallel runs each line it reads.
	if name == "parallel" && !slices.ContainsFunc(args, func(word shellWord) bool { return !strings.HasPrefix(word.text, "-") }) {
		g.unsure(at)
	}
}

// interpreter is how a program that runs code takes it.
type interpreter struct {
	// letters are its options that take code, standard says it runs what its input holds when it gets no file, and
	// module that `-m` ends its options.
	letters  string
	standard bool
	module   bool
}

var interpreters = map[string]interpreter{
	"python": {letters: "c", standard: true, module: true}, "pypy": {letters: "c", standard: true, module: true},
	"node": {letters: "ep", standard: true}, "nodejs": {letters: "ep", standard: true}, "bun": {letters: "e"},
	"deno": {}, "ruby": {letters: "e", standard: true}, "perl": {letters: "eE", standard: true},
	"php": {letters: "r", standard: true}, "lua": {letters: "e", standard: true}, "osascript": {letters: "e", standard: true},
	"rscript": {letters: "e"}, "pwsh": {letters: "c", standard: true}, "tclsh": {standard: true}, "expect": {letters: "c"},
	"awk": {}, "gawk": {}, "mawk": {}, "nawk": {}, "sed": {},
}

func interpreterOf(name string) (interpreter, bool) {
	kind, found := interpreters[strings.TrimRight(name, "0123456789.")]
	return kind, found
}

// code reads what an interpreter runs for a mention of a push or a gate, because this reads no language but a
// shell's. Starts says the interpreter is the command.
func (g *shellGates) code(kind interpreter, args []shellWord, at shellPlace, starts bool) {
	if !g.spend(args) {
		return
	}
	for _, word := range args {
		if strings.ContainsAny(word.text, shellSpecial) {
			g.mentions(word.text)
		}
	}
	if !starts {
		return
	}
	coded, file, standard := false, false, len(args) == 0
	for i, word := range args {
		text := word.text
		if readsStandardInput(text) {
			standard = true
			break
		}
		if kind.module && strings.HasPrefix(text, "-m") || !strings.HasPrefix(text, "-") {
			file = true
			break
		}
		if text == "--version" || text == "--help" || text == "-V" {
			return
		}
		takes := slices.Contains([]string{"--eval", "--print", "--command"}, text) ||
			(!strings.HasPrefix(text, "--") && kind.letters != "" && strings.ContainsAny(text[1:], kind.letters))
		if flag, _, joined := strings.Cut(text, "="); joined && slices.Contains([]string{"--eval", "--print"}, flag) {
			takes = true
		}
		if !takes {
			continue
		}
		coded = true
		if word.expands || (i+1 < len(args) && args[i+1].expands) {
			g.unsure(at)
		}
		break
	}
	for _, body := range at.command.fed {
		if g.work += len(body.text); g.work > maxGateWork {
			g.unread = true
			return
		}
		g.mentions(body.text)
	}
	if kind.standard && !coded && (standard || (at.command.piped && !file)) {
		if len(at.command.fed) == 0 || slices.ContainsFunc(at.command.fed, func(body fedText) bool { return body.expands }) {
			g.unsure(at)
		}
	}
}

func codeWords(text string) []string {
	return strings.FieldsFunc(text, func(c rune) bool {
		return c < 0x80 && !isNameCharacter(byte(c)) && !strings.ContainsRune("./-", c)
	})
}

// mentions finds in code a push, as git and a later push, or a gate, as its words in their order.
func (g *shellGates) mentions(code string) {
	words := codeWords(code)
	git := false
	for _, word := range words {
		name := strings.ToLower(path.Base(word))
		if (git && word == "push") || slices.Contains(pushPrograms, name) {
			g.pushed = true
		}
		git = git || name == "git"
	}
	for _, gate := range g.gates {
		found := 0
		for _, word := range words {
			if found < len(gate.fields) && (word == gate.fields[found] || path.Base(word) == path.Base(gate.fields[found])) {
				found++
			}
		}
		if found == len(gate.fields) && found != 0 {
			g.gated = true
		}
	}
}

// showsPush says a command's words, with their quotes and escapes taken away, hold one that names a push.
func showsPush(command string) bool {
	plain := strings.NewReplacer("\\\n", "", `\`, "", "'", "", `"`, "").Replace(command)
	return slices.ContainsFunc(codeWords(plain), func(word string) bool {
		return slices.Contains(pushSubcommands, word) || slices.Contains(pushPrograms, path.Base(word))
	})
}

// dataPrograms take their arguments and their input as data.
var dataPrograms = strings.Fields(`
	echo printf print grep egrep fgrep zgrep ack ag cat tee head tail wc ls stat which whereis type test [ jq man cd pwd
	export declare local readonly typeset unset read mkdir touch rm rmdir cp mv basename dirname diff cut tr uniq`)

// shellSpecial are the bytes that make a word worth reading as a command of its own.
const shellSpecial = " \t\r\n;&|()<>$`'\"\\"

// search reads the words of a program this does not know, any of which could be where a command starts.
func (g *shellGates) search(words []shellWord, at shellPlace) {
	if at.loose {
		return
	}
	for i, word := range words {
		if g.work > maxGateWork {
			g.unread = true
			return
		}
		rest := words[i+1:]
		if len(rest) != 0 && rest[0].text == "push" && (word.expands || (word.glob && couldBeGit(word.text))) {
			g.unsure(at)
		}
		if !word.expands {
			name := strings.ToLower(path.Base(strings.TrimPrefix(word.text, "=")))
			switch {
			case name == "git":
				g.git(rest, at, false)
			case slices.Contains(pushPrograms, name):
				g.push(rest, at)
			case slices.Contains(gateShells, name):
				if len(rest) != 0 && strings.HasPrefix(rest[0].text, "-") && strings.Contains(rest[0].text, "c") {
					g.shell(rest, at.deeper())
				}
			default:
				if kind, runs := interpreterOf(name); runs {
					g.code(kind, rest, at, false)
				}
			}
			g.matchWords(name, rest, at, false)
		}
		value := word.text
		if flag, held, joined := strings.Cut(value, "="); joined && strings.HasPrefix(flag, "-") {
			value = held
		}
		if strings.ContainsAny(value, shellSpecial) {
			g.script(value, shellPlace{depth: at.depth + 1, loose: true, varying: at.varying || at.input || word.expands})
		}
	}
}

func couldBeGit(pattern string) bool {
	if braced(pattern) {
		return true
	}
	matched, err := path.Match(path.Base(pattern), "git")
	return err == nil && matched
}

// matchLine matches each gate against a command as written, which is the one way to read a gate with a `*` inside it.
func (g *shellGates) matchLine(words []shellWord) {
	if len(g.gates) == 0 {
		return
	}
	line := joinWords(words)
	for _, gate := range g.gates {
		rest, matched := line, true
		for i, part := range strings.Split(gate.prefix, "*") {
			at := strings.Index(rest, part)
			if at < 0 || (i == 0 && at != 0) {
				matched = false
				break
			}
			rest = rest[at+len(part):]
		}
		g.gated = g.gated || matched
	}
}

// optionValue is what may vary as the value of an option known to take one.
type optionValue int

const (
	// valueNumber is only a substitution that prints a number, as make's `-j` takes no other word as its value.
	valueNumber optionValue = iota + 1
	// valueWord is one word, which no shell splits.
	valueWord
	// valueSplit is a word a shell may split, with another after it.
	valueSplit
)

// gateValues are the options known to take a value where they stand, for each program, and for git for each
// subcommand. A word that varies is none of a gate's words only as the value of one of these. Every other fails closed.
var gateValues = map[string]map[string]optionValue{
	"make":       makeValues,
	"gmake":      makeValues,
	"just":       {"-f": valueWord, "--justfile": valueWord},
	"rake":       {"-f": valueWord},
	"ninja":      {"-f": valueWord, "-C": valueWord, "-j": valueNumber},
	"npm":        {"--prefix": valueSplit},
	"git commit": {"-m": valueWord, "--message": valueWord},
	"git merge":  {"-m": valueWord},
}

var makeValues = map[string]optionValue{
	"-j": valueNumber, "-l": valueNumber, "--jobs": valueNumber,
	"-C": valueWord, "--directory": valueWord, "-f": valueWord, "--file": valueWord,
}

// failsClosed says a word of args varies that is no value gateValues knows. It infers no value from a dash or from
// quotes: an option it does not know may take the next word, a known option included, so that one exempts nothing.
func failsClosed(program string, args []shellWord, replace string) bool {
	values := gateValues[program]
	known, options := true, true
	for i := 0; i < len(args); i++ {
		word := args[i]
		option := options && !word.expands && strings.HasPrefix(word.text, "-")
		if option && word.text == "--" {
			options = false
			continue
		}
		if kind := values[word.text]; option && kind != 0 && i+1 < len(args) {
			held := args[i+1]
			placed := known && held.expands && !held.glob && !held.manyWords() && (replace == "" || !strings.Contains(held.text, replace))
			switch {
			case kind == valueNumber && held.counts && held.text == "":
				known = true
				i++
				continue
			case placed && ((kind == valueWord && !held.split) || (kind == valueSplit && i+2 < len(args))):
				// One that splits may be no word, and the option then takes the next.
				known = !held.split
				i++
				continue
			}
		}
		if varies(word, replace) {
			joined := values[strings.TrimSuffix(word.text, "=")] == valueNumber && word.counts &&
				word.head == len(word.text) && word.tail == len(word.text)
			if !joined {
				return true
			}
			known = true
			continue
		}
		known = !option || (strings.HasPrefix(word.text, "--") && strings.Contains(word.text, "="))
	}
	return false
}

// matchWords matches each gate against a program and its arguments. Where a gate is configured for the program, a
// word that varies, or words this cannot read, are the gate's (failsClosed). Starts says the program is the command.
func (g *shellGates) matchWords(name string, args []shellWord, at shellPlace, starts bool) {
	counted := false
	for _, gate := range g.gates {
		if len(gate.fields) == 0 {
			continue
		}
		program := strings.ToLower(path.Base(gate.fields[0]))
		anyProgram := strings.Contains(program, "*")
		switch {
		case anyProgram:
			if matched, err := path.Match(program, name); !starts || err != nil || !matched {
				continue
			}
		case program != name:
			continue
		}
		// git's options are not its subcommand's, so matchGit reads them.
		if program == "git" && len(gate.fields) > 1 {
			continue
		}
		if !counted {
			if !g.spend(args) {
				return
			}
			counted = true
		}
		g.gated = g.gated || at.input || at.varying || failsClosed(name, args, at.replace) ||
			(!strings.Contains(gate.prefix, "*") && followsGate(name, gate.fields[1:], args))
	}
}

// targetRunners take several targets, so a gated one can follow another.
var targetRunners = []string{"make", "gmake", "just", "rake", "ninja"}

// followsGate says args, whose values failsClosed read, hold the rest of a gate's words in their order. An option, a
// word after one, and a `NAME=value` can stand between them, which only ever lengthens the search, and so can a
// target where the program takes several, or any word where a value a shell splits moved the rest along.
func followsGate(name string, fields []string, args []shellWord) bool {
	several := slices.Contains(targetRunners, name) || slices.ContainsFunc(args, func(word shellWord) bool { return word.split })
	at, valued := 0, false
	for i, field := range fields {
		last := i+1 == len(fields)
		if strings.HasPrefix(field, "-") {
			if !slices.ContainsFunc(args, func(word shellWord) bool { return word.expands || holdsOption(word.text, field) }) {
				return false
			}
			continue
		}
		for {
			if at >= len(args) {
				return false
			}
			word := args[at]
			at++
			if word.text == field || (last && strings.HasPrefix(word.text, field)) {
				valued = false
				break
			}
			if strings.HasPrefix(word.text, "-") {
				valued = !strings.Contains(word.text, "=")
				continue
			}
			if valued {
				valued = false
				continue
			}
			if !isAssignment(word.text) && !several {
				return false
			}
		}
	}
	return true
}

// holdsOption says a word sets an option, by itself, with its value joined, or in a cluster of short ones.
func holdsOption(text, option string) bool {
	if strings.HasPrefix(text, option) {
		return true
	}
	if strings.HasPrefix(option, "--") || strings.HasPrefix(text, "--") || !strings.HasPrefix(text, "-") {
		return false
	}
	for _, letter := range option[1:] {
		if !strings.ContainsRune(text, letter) {
			return false
		}
	}
	return true
}

// matchGit matches each gate against git's subcommand and its arguments, which take options in any order. Where a
// gate is configured for the subcommand, a word that varies fails closed as it does for any program (failsClosed).
func (g *shellGates) matchGit(args []shellWord, at shellPlace) {
	if len(g.gates) == 0 {
		return
	}
	g.matchLine(append([]shellWord{{text: "git"}}, args...))
	subcommand := args[0].text
	for _, gate := range g.gates {
		if len(gate.fields) < 2 || !strings.EqualFold(path.Base(gate.fields[0]), "git") {
			continue
		}
		fields := gate.fields[1:]
		written := !strings.Contains(gate.prefix, "*")
		configured := fields[0] == subcommand || strings.HasPrefix(fields[0], "-") ||
			(len(fields) == 1 && strings.HasPrefix(subcommand, fields[0]))
		if strings.Contains(fields[0], "*") {
			matched, err := path.Match(fields[0], subcommand)
			configured = err == nil && matched
		}
		if !configured {
			continue
		}
		if at.input || at.varying || failsClosed("git "+subcommand, args[1:], at.replace) {
			g.gated = true
			return
		}
		// matchLine reads a gate with a `*`, or one that starts with git's own options.
		if !written || strings.HasPrefix(fields[0], "-") {
			continue
		}
		found := 1
		for _, word := range args[1:] {
			if found == len(fields) {
				break
			}
			field := fields[found]
			if word.text == field || (found+1 == len(fields) && strings.HasPrefix(word.text, field)) ||
				(strings.HasPrefix(field, "-") && holdsOption(word.text, field)) {
				found++
			}
		}
		g.gated = g.gated || found == len(fields)
	}
}
