package lanes

import (
	"path"
	"slices"
	"strings"
)

// readHere says where a reader reads when it names no path. Its zero value reads its input and no directory.
type readHere int

const (
	// readsWhereItStands reads the directory the session stands in.
	readsWhereItStands readHere = iota + 1
	// readsWhereItStandsRecursively does so only with a recursive option.
	readsWhereItStandsRecursively
)

// readerRule says how one program that reads files takes its operands.
type readerRule struct {
	here readHere
	// pattern says the first operand is a pattern or a script, which no option supplied, and so no path.
	pattern bool
	// supplies are the options whose value is the pattern or script, files are the options whose value is a file
	// the program reads, and bare are the options that need no pattern. valued are the options whose value is no
	// path.
	supplies, files, bare, valued []string
	// recursive are the options that make a search read the directory it stands in.
	recursive []string
	// lastIsTarget says the last operand is where the program writes, so only the operands before it are read, unless
	// an option of targets names the destination, which leaves every operand a source.
	lastIsTarget bool
	targets      []string
}

var (
	countOptions  = []string{"-n", "--lines", "--bytes"}
	searchOptions = []string{"-A", "-B", "-C", "-m", "-g", "-t", "-T", "-d", "-j", "-M", "--glob", "--type", "--type-not", "--max-count", "--max-depth",
		"--context", "--after-context", "--before-context", "--threads", "--include", "--exclude", "--exclude-dir"}
	searchSupplies = []string{"-e", "--regexp"}
	searchFiles    = []string{"-f", "--file"}
)

// readers are the programs a hook reads for the files they read: the ones an agent runs to look at a file or a
// directory. A program not listed reads where its own code says, and a hook does not guess.
var readers = map[string]readerRule{
	"cat": {}, "tac": {}, "less": {}, "more": {}, "bat": {}, "batcat": {}, "nl": {}, "wc": {}, "file": {}, "stat": {}, "strings": {},
	"od": {}, "xxd": {}, "hexdump": {}, "sort": {}, "uniq": {}, "cut": {}, "diff": {}, "cmp": {}, "md5sum": {}, "sha1sum": {},
	"sha256sum": {}, "shasum": {}, "base64": {}, "realpath": {}, "readlink": {},
	"head": {valued: countOptions}, "tail": {valued: countOptions},
	"ls": {here: readsWhereItStands}, "eza": {here: readsWhereItStands}, "exa": {here: readsWhereItStands},
	"tree": {here: readsWhereItStands}, "du": {here: readsWhereItStands},
	"grep":   {pattern: true, supplies: searchSupplies, files: searchFiles, valued: searchOptions, here: readsWhereItStandsRecursively, recursive: []string{"-r", "-R", "--recursive", "--dereference-recursive"}},
	"egrep":  {pattern: true, supplies: searchSupplies, files: searchFiles, valued: searchOptions, here: readsWhereItStandsRecursively, recursive: []string{"-r", "-R", "--recursive"}},
	"fgrep":  {pattern: true, supplies: searchSupplies, files: searchFiles, valued: searchOptions, here: readsWhereItStandsRecursively, recursive: []string{"-r", "-R", "--recursive"}},
	"rg":     {pattern: true, supplies: searchSupplies, files: searchFiles, bare: []string{"--files", "--type-list"}, valued: searchOptions, here: readsWhereItStands},
	"ag":     {pattern: true, supplies: searchSupplies, files: searchFiles, valued: searchOptions, here: readsWhereItStands},
	"ack":    {pattern: true, supplies: searchSupplies, files: searchFiles, valued: searchOptions, here: readsWhereItStands},
	"fd":     {pattern: true, valued: []string{"-d", "-e", "-E", "-t", "-S", "--max-depth", "--extension", "--exclude", "--type", "--size"}, here: readsWhereItStands},
	"fdfind": {pattern: true, valued: []string{"-d", "-e", "-E", "-t", "-S", "--max-depth", "--extension", "--exclude", "--type", "--size"}, here: readsWhereItStands},
	"sed":    {pattern: true, supplies: []string{"-e", "--expression"}, files: []string{"-f", "--file"}},
	"awk":    {pattern: true, files: []string{"-f", "--file"}, valued: []string{"-F", "-v"}},
	"gawk":   {pattern: true, files: []string{"-f", "--file"}, valued: []string{"-F", "-v"}},
	"jq":     {pattern: true, files: []string{"-f", "--from-file"}, valued: []string{"--arg", "--argjson"}},
	"cp":     {lastIsTarget: true, targets: copyTargets}, "install": {lastIsTarget: true, targets: copyTargets, valued: []string{"-m", "-o", "-g", "--mode", "--owner", "--group"}},
	"rsync": {lastIsTarget: true, valued: []string{"-e", "--rsh", "--exclude", "--include", "--port", "--bwlimit"}},
	"tar":   {files: []string{"-f", "--file"}, valued: []string{"-C", "--directory", "--exclude"}},
}

// copyTargets are the options that name the directory a copy goes to.
var copyTargets = []string{"-t", "--target-directory"}

// readOperands are the words a reader takes as paths, and whether the reader was given an option that reads the
// directory it stands in with no path.
func readOperands(name string, rule readerRule, args []shellWord) (paths []shellWord, recursive bool) {
	if name == "find" {
		return findOperands(args), true
	}
	needPattern := rule.pattern
	options, targeted := true, false
	for i := 0; i < len(args); i++ {
		word := args[i]
		text := word.text
		switch {
		case options && text == "--":
			options = false
		case options && strings.HasPrefix(text, "-") && len(text) > 1:
			option, value, joined := strings.Cut(text, "=")
			switch {
			case slices.Contains(rule.recursive, option):
				recursive = true
			case slices.Contains(rule.targets, option):
				targeted = true
				if !joined {
					i++
				}
			case slices.Contains(rule.bare, option):
				needPattern = false
			case slices.Contains(rule.supplies, option):
				needPattern = false
				if !joined {
					i++
				}
			case slices.Contains(rule.files, option):
				needPattern = false
				switch {
				case joined:
					paths = append(paths, shellWord{text: value, dynamic: word.dynamic})
				case i+1 < len(args):
					paths = append(paths, args[i+1])
					i++
				}
			case slices.Contains(rule.valued, option) && !joined:
				i++
			default:
				if bundled(text, rule.recursive) {
					recursive = true
				}
			}
		case needPattern:
			needPattern = false
		default:
			paths = append(paths, word)
		}
	}
	if rule.lastIsTarget && !targeted && len(paths) != 0 {
		paths = paths[:len(paths)-1]
	}
	return paths, recursive
}

// bundled says a bundle of short options, such as `-rn`, holds one of the recursive options.
func bundled(text string, recursive []string) bool {
	if strings.HasPrefix(text, "--") {
		return false
	}
	for _, letter := range text[1:] {
		if slices.Contains(recursive, "-"+string(letter)) {
			return true
		}
	}
	return false
}

// findOperands are the places find searches: the words before its first expression, or the directory it stands in.
func findOperands(args []shellWord) []shellWord {
	i := 0
	for i < len(args) {
		switch args[i].text {
		case "-H", "-L", "-P":
			i++
		case "-D", "-O":
			i += 2
		default:
			var paths []shellWord
			for ; i < len(args); i++ {
				text := args[i].text
				if strings.HasPrefix(text, "-") || text == "(" || text == "!" || text == ")" {
					break
				}
				paths = append(paths, args[i])
			}
			return paths
		}
	}
	return nil
}

// readContext is what a reader's call stands in: whether xargs runs it, the one command that feeds it through a
// pipe when the call is those two commands and nothing else, and the loop variables the call has bound.
type readContext struct {
	xargs  bool
	source *shellCommand
	vars   *loopVars
}

// loopVars are the words a `for` loop lists for its variable, while the loop's body runs. They are the only variables a
// read resolves: a variable the call assigned, or one a command in a loop body may change, is a word only a shell can
// expand, and a read of one asks.
type loopVars struct {
	words map[string][]string
	depth int
}

// loopStarts are the words that open a loop, which `done` closes.
var loopStarts = []string{"for", "while", "until", "select"}

// variableMutators are the programs that can set a variable, so a loop body that runs one holds no binding.
var variableMutators = []string{
	"export", "declare", "typeset", "local", "readonly", "eval", "source", ".", "read", "printf", "mapfile", "readarray", "getopts",
	"let", "unset", "set", "shopt", "exec", "trap", "alias", "command", "builtin", "env",
}

// note takes what a command does to the loops it stands in. A `for` binds its name to the words it lists, and unbinds it
// when one of them expands or the loop has no name. `done` ends the loop and every binding. A command in a loop body
// that has a word with `=` in it, runs a program that can set a variable, or runs a program a shell expands, unbinds
// every name, because nothing here reads what it sets.
func (v *loopVars) note(command shellCommand) {
	words := command.words
	if len(words) == 0 {
		return
	}
	// A loop starts or ends after the keyword that opens a body, as `do for g in ...` does.
	for len(words) > 1 && !words[0].quoted && slices.Contains(shellKeywords, words[0].text) && words[0].text != "done" {
		words = words[1:]
	}
	first := words[0]
	switch {
	case !first.quoted && slices.Contains(loopStarts, first.text):
		v.depth++
		if first.text != "for" {
			return
		}
		if len(words) < 4 || words[2].text != "in" || words[2].quoted || !isNameWord(words[1].text) {
			clear(v.words)
			return
		}
		listed := make([]string, 0, len(words)-3)
		for _, word := range words[3:] {
			if word.dynamic || word.expands {
				delete(v.words, words[1].text)
				return
			}
			listed = append(listed, word.text)
		}
		v.words[words[1].text] = listed
		return
	case !first.quoted && first.text == "done":
		if v.depth > 0 {
			v.depth--
		}
		if v.depth == 0 {
			clear(v.words)
		}
		return
	}
	if v.depth > 0 && changesVariables(command) {
		clear(v.words)
	}
}

// changesVariables says a command may set a variable.
func changesVariables(command shellCommand) bool {
	if slices.ContainsFunc(command.words, func(word shellWord) bool {
		return strings.Contains(word.text, "=") || strings.HasPrefix(word.text, "((")
	}) {
		return true
	}
	for _, reading := range leaderReadings(command.words) {
		if reading[0].expands || slices.Contains(variableMutators, path.Base(reading[0].text)) {
			return true
		}
	}
	return false
}

// isNameWord says text is a variable's name.
func isNameWord(text string) bool {
	return text != "" && !slices.ContainsFunc([]byte(text), func(c byte) bool { return !isNameCharacter(c) }) && (text[0] < '0' || text[0] > '9')
}

// expand reads a word that is a loop variable, or one and a path that follows it, as the words the loop lists. It
// returns none for any other word.
func (v *loopVars) expand(word shellWord) []shellWord {
	if v == nil || len(v.words) == 0 || !strings.HasPrefix(word.text, "$") {
		return nil
	}
	name, rest := strings.TrimPrefix(word.text, "$"), ""
	if braced, ok := strings.CutPrefix(name, "{"); ok {
		inside, after, found := strings.Cut(braced, "}")
		if !found {
			return nil
		}
		name, rest = inside, after
	} else if at := strings.IndexFunc(name, func(r rune) bool { return r > 0x7f || !isNameCharacter(byte(r)) }); at >= 0 {
		name, rest = name[:at], name[at:]
	}
	held, bound := v.words[name]
	if !bound || (rest != "" && rest[0] != '/') || strings.Contains(rest, "$") {
		return nil
	}
	out := make([]shellWord, 0, len(held))
	for _, value := range held {
		out = append(out, shellWord{text: value + rest, glob: strings.ContainsAny(value, "*?[{")})
	}
	return out
}

// programReadsOutside says a program that reads files reads one where AskReadRoots do not reach, as far as its
// words say: a path it names, or the directory the session stands in when it names none. A path only a shell can
// name reads as outside, because a hook runs nothing and cannot say where it leads, unless a `for` loop over static
// words holds it. A reader run through xargs reads what the command before it names, which is inside only for the
// listings xargsReadsOutside proves. A directory the policy does not know reads as inside. A program that runs the
// code its words hold reads where that code names a place (codeReadsOutside), and one that makes a link reads where
// the link leads, which no hook sees.
func (p DenyPolicy) programReadsOutside(name string, args []shellWord, at string, command shellCommand, in readContext) bool {
	if name == "ln" {
		return true
	}
	if kind, runs := interpreterOf(name); runs && kind.letters != "" {
		return p.codeReadsOutside(name, kind, args, at, command, in.vars)
	}
	rule, listed := readers[name]
	if !listed && name != "find" {
		return false
	}
	paths, recursive := readOperands(name, rule, args)
	for _, word := range paths {
		if p.readsOutside(word, at, in.vars) {
			return true
		}
	}
	if in.xargs {
		return rule.lastIsTarget || name == "find" || in.source == nil || !p.listingInside(*in.source, at)
	}
	if len(paths) != 0 || at == "" || command.piped && name != "find" {
		return false
	}
	here := rule.here == readsWhereItStands || rule.here == readsWhereItStandsRecursively && recursive
	if name == "find" {
		here = true
	}
	return here && !p.leadsUnder(at, at, at, p.AskReadRoots, true)
}

// xargsReads says what an xargs call hands a reader may lie outside. xargs is the program of the call, with no wrapper
// before it, and takes only the options of xargsRest. Its program, when it reads files, reads inside only when the call
// is the one command that lists files and xargs, and the listing is one of the three listingInside proves.
func (p DenyPolicy) xargsReads(command shellCommand, at string, in readContext) bool {
	words := command.words
	start := slices.IndexFunc(words, func(word shellWord) bool {
		return word.quoted || !slices.Contains(shellKeywords, word.text) && !isAssignment(word.text)
	})
	if start < 0 || words[start].quoted || words[start].dynamic || path.Base(words[start].text) != "xargs" {
		return true
	}
	rest, ok := xargsRest(words[start+1:])
	if !ok {
		return true
	}
	if len(rest) == 0 {
		return false
	}
	if rest[0].dynamic || rest[0].expands {
		return true
	}
	in.xargs = true
	return p.programReadsOutside(path.Base(rest[0].text), rest[1:], at, command, in)
}

// xargsRest takes the options xargs may be given and returns the words after them: -0 and --null, -r and
// --no-run-if-empty, -n, -P, and -L with a number, and -d with a delimiter, each as one word or, for the long forms,
// joined with `=`. They change how xargs splits and batches what it reads. Any other option, and any abbreviation of a
// long one, reads from or runs somewhere no hook can read, and says the call is none to allow.
func xargsRest(words []shellWord) ([]shellWord, bool) {
	number := func(word shellWord) bool {
		return !word.dynamic && !word.expands && word.text != "" && len(word.text) <= 6 &&
			!slices.ContainsFunc([]byte(word.text), func(c byte) bool { return c < '0' || c > '9' })
	}
	i := 0
	for ; i < len(words); i++ {
		word := words[i]
		text := word.text
		if !strings.HasPrefix(text, "-") || word.quoted && text == "-" {
			break
		}
		if word.dynamic || word.expands || word.glob {
			return nil, false
		}
		switch {
		case text == "-0", text == "--null", text == "-r", text == "--no-run-if-empty":
		case text == "-n", text == "-P", text == "-L":
			if i++; i >= len(words) || !number(words[i]) {
				return nil, false
			}
		case text == "-d":
			if i++; i >= len(words) || words[i].dynamic || words[i].expands || len(words[i].text) == 0 || len(words[i].text) > 2 {
				return nil, false
			}
		case strings.HasPrefix(text, "--max-args="), strings.HasPrefix(text, "--max-procs="), strings.HasPrefix(text, "--max-lines="):
			_, value, _ := strings.Cut(text, "=")
			if !number(shellWord{text: value}) {
				return nil, false
			}
		case strings.HasPrefix(text, "--delimiter="):
			_, value, _ := strings.Cut(text, "=")
			if len(value) == 0 || len(value) > 2 {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return words[i:], true
}

// listingInside says a command lists only regular files that lie inside AskReadRoots, and no link: one of the three
// listings below, with only the flags named for it, and nothing else. A flag not named, an expansion, or a program that
// is no one of them is no listing.
func (p DenyPolicy) listingInside(command shellCommand, at string) bool {
	words := command.words
	if len(words) == 0 || len(command.reads)+len(command.writes)+len(command.processes) != 0 || command.heredoc || len(command.fed) != 0 || command.piped {
		return false
	}
	if slices.ContainsFunc(words, func(word shellWord) bool { return word.dynamic || word.expands || word.glob || word.process }) {
		return false
	}
	args := words[1:]
	switch path.Base(words[0].text) {
	case "rg":
		return p.rgListsInside(args, at)
	case "fd":
		return p.fdListsInside(args, at)
	case "find":
		return p.findListsInside(args, at)
	}
	return false
}

// valueOf takes the word after the option at i as its value.
func valueOf(args []shellWord, i int) (string, bool) {
	if i+1 >= len(args) {
		return "", false
	}
	return args[i+1].text, true
}

// rgListsInside says an rg lists the files it would search, or the files that match a pattern, in places inside, with only
// flags that filter by pattern, type, or glob. rg skips links unless it is told to follow them, and prints a name a
// line unless it is told otherwise, which no allowed flag does.
func (p DenyPolicy) rgListsInside(args []shellWord, at string) bool {
	files, lists, pattern := false, false, false
	var operands []shellWord
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "--files":
			files = true
		case text == "-l":
			lists = true
		case text == "-i", text == "-F", text == "-w":
		case text == "-t", text == "-T", text == "-g", text == "--type", text == "--glob", text == "-e":
			if _, ok := valueOf(args, i); !ok {
				return false
			}
			pattern = pattern || text == "-e"
			i++
		case strings.HasPrefix(text, "--type="), strings.HasPrefix(text, "--glob="):
		case strings.HasPrefix(text, "-"):
			return false
		default:
			operands = append(operands, args[i])
		}
	}
	if files == lists || files && pattern {
		return false
	}
	if lists && !pattern {
		if len(operands) == 0 {
			return false
		}
		operands = operands[1:]
	}
	return p.placesInside(operands, at)
}

// fdListsInside says an fd lists files, and only files, that match a name or an extension, in places inside. fd skips
// links unless it is told to follow them.
func (p DenyPolicy) fdListsInside(args []shellWord, at string) bool {
	types := 0
	var operands []shellWord
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "-t", text == "--type":
			if value, ok := valueOf(args, i); !ok || value != "f" {
				return false
			}
			types++
			i++
		case text == "-e", text == "-E", text == "-d":
			if _, ok := valueOf(args, i); !ok {
				return false
			}
			i++
		case text == "-g", text == "-H", text == "-I":
		case strings.HasPrefix(text, "-"):
			return false
		default:
			operands = append(operands, args[i])
		}
	}
	if types != 1 {
		return false
	}
	if len(operands) != 0 {
		operands = operands[1:]
	}
	return p.placesInside(operands, at)
}

// findListsInside says a find lists regular files, and only files, in places inside, and takes its names from nothing but
// tests on their name and path, and the depth it descends. A find follows no link unless it is told to, a test of type
// f leaves out the links, and an action but the last print runs a program or writes a file.
func (p DenyPolicy) findListsInside(args []shellWord, at string) bool {
	var roots []shellWord
	i := 0
	for ; i < len(args) && !strings.HasPrefix(args[i].text, "-") && args[i].text != "!"; i++ {
		roots = append(roots, args[i])
	}
	files := 0
	for ; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "-type":
			if value, ok := valueOf(args, i); !ok || value != "f" {
				return false
			}
			files++
			i++
		case text == "-name", text == "-iname", text == "-path", text == "-ipath":
			if _, ok := valueOf(args, i); !ok {
				return false
			}
			i++
		case text == "-maxdepth", text == "-mindepth":
			if value, ok := valueOf(args, i); !ok || value == "" || slices.ContainsFunc([]byte(value), func(c byte) bool { return c < '0' || c > '9' }) {
				return false
			}
			i++
		case text == "-not", text == "!":
		case (text == "-print" || text == "-print0") && i == len(args)-1:
		default:
			return false
		}
	}
	return files == 1 && p.placesInside(roots, at)
}

// placesInside says every place a listing names lies inside, and with none it lists where the session stands.
func (p DenyPolicy) placesInside(places []shellWord, at string) bool {
	for _, word := range places {
		if p.readsOutside(word, at, nil) {
			return false
		}
	}
	return len(places) != 0 || at != "" && p.leadsUnder(at, at, at, p.AskReadRoots, true)
}

// codeReadsOutside says the code an interpreter is given names a place where AskReadRoots do not reach: inline code,
// as `python -c` takes it, or a here-document or here-string that feeds it. Code names a path as a word that begins
// with `/`, `~`, or `..`, which is as far as a hook reads a language it does not run. Code only a shell can write, or
// standard input a pipe feeds with nothing to read, asks, as the gates treat code they cannot read. A script file is
// the lane's own, and a module is no path.
func (p DenyPolicy) codeReadsOutside(name string, kind interpreter, args []shellWord, at string, command shellCommand, vars *loopVars) bool {
	var code, rest []shellWord
	file, coded := false, false
	for i := 0; i < len(args) && !file; i++ {
		text := args[i].text
		switch {
		case text == "-" || text == "--":
			file = false
			i = len(args)
		case kind.module && strings.HasPrefix(text, "-m") || !strings.HasPrefix(text, "-"):
			file = true
		case slices.Contains([]string{"--eval", "--print", "--command"}, text) || !strings.HasPrefix(text, "--") && strings.ContainsAny(text[1:], kind.letters):
			coded = true
			if i+1 < len(args) {
				code = append(code, args[i+1])
				rest = args[i+2:]
			}
			i++
		}
		if coded {
			break
		}
	}
	if file {
		return false
	}
	for _, word := range code {
		if word.dynamic || word.expands || p.namesPlaceOutside(word.text, at) {
			return true
		}
	}
	// A script that reads its input, as perl and ruby do with -n and -p, reads the files that follow its code.
	if base := strings.TrimRight(name, "0123456789."); base == "perl" || base == "ruby" {
		for _, word := range rest {
			if !strings.HasPrefix(word.text, "-") && p.readsOutside(word, at, vars) {
				return true
			}
		}
	}
	if coded {
		return false
	}
	for _, body := range command.fed {
		if body.expands || p.namesPlaceOutside(body.text, at) {
			return true
		}
	}
	return kind.standard && command.piped && len(command.fed) == 0
}

// namesPlaceOutside says text names, as a word, a path where AskReadRoots do not reach.
func (p DenyPolicy) namesPlaceOutside(text, at string) bool {
	for word := range strings.FieldsFuncSeq(text, func(c rune) bool {
		return (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("_./~$-+@:%", c)
	}) {
		if !strings.HasPrefix(word, "/") && !strings.HasPrefix(word, "~") && !strings.HasPrefix(word, "../") && word != ".." {
			continue
		}
		if strings.Trim(word, "/") == "" || strings.HasPrefix(word, "//") {
			continue
		}
		if p.readsOutside(shellWord{text: word}, at, nil) {
			return true
		}
	}
	return false
}

// readsOutside says a file a command reads, as the words give its name, lies where AskReadRoots do not reach. A name
// only a shell can expand reads as outside.
func (p DenyPolicy) readsOutside(word shellWord, at string, vars *loopVars) bool {
	if held := vars.expand(word); held != nil {
		return slices.ContainsFunc(held, func(word shellWord) bool { return p.readsOutside(word, at, nil) })
	}
	resolved, ok := p.resolve(word, at)
	if !ok {
		// A word only a shell can expand names a place no hook can read.
		return word.dynamic || word.expands
	}
	if slices.Contains(quietDevices, resolved) || word.process || resolved == "/dev/stdin" {
		return false
	}
	raw := word.text
	if strings.HasPrefix(raw, "~") || strings.Contains(raw, "$") {
		raw = resolved
	}
	return !p.leadsUnder(raw, resolved, at, p.AskReadRoots, true)
}
