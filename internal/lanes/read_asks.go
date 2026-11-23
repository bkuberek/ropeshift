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
	"sed":    {pattern: true, supplies: []string{"-e", "--expression"}, files: []string{"--file"}},
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

// readContext is what a reader's call stands in: the words before its program, the command a pipe feeds it from, and
// the variables the call assigned with static words before it.
type readContext struct {
	leaders  []shellWord
	previous *shellCommand
	vars     shellVars
}

// shellVars are the variables a call assigned a static word, or the static words of a `for` loop, before the command
// that reads them. A variable assigned from anything else is no entry, so it reads as a word only a shell can expand.
type shellVars map[string][]string

// note takes what a command assigns. A call that is only assignments binds each name to its word, and one whose word a
// shell expands unbinds it. A `for` binds its name to the words it lists, and unbinds it when one of them expands.
func (v shellVars) note(command shellCommand) {
	words := command.words
	switch {
	case len(words) == 0:
	case len(words) >= 3 && words[0].text == "for" && !words[0].quoted && words[2].text == "in" && !words[2].quoted && isNameWord(words[1].text):
		static := true
		listed := make([]string, 0, len(words)-3)
		for _, word := range words[3:] {
			static = static && !word.dynamic && !word.expands
			listed = append(listed, word.text)
		}
		if static && len(listed) != 0 {
			v[words[1].text] = listed
		} else {
			delete(v, words[1].text)
		}
	case !slices.ContainsFunc(words, func(word shellWord) bool { return !isAssignment(word.text) }):
		for _, word := range words {
			name, value, _ := strings.Cut(word.text, "=")
			if word.dynamic || word.expands || word.split {
				delete(v, name)
			} else {
				v[name] = []string{value}
			}
		}
	}
}

// isNameWord says text is a variable's name.
func isNameWord(text string) bool {
	return text != "" && !slices.ContainsFunc([]byte(text), func(c byte) bool { return !isNameCharacter(c) }) && (text[0] < '0' || text[0] > '9')
}

// expand reads a word that is a variable, or a variable and a path that follows it, as the static words it holds. It
// returns none for any other word.
func (v shellVars) expand(word shellWord) []shellWord {
	if len(v) == 0 || !strings.HasPrefix(word.text, "$") {
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
	held, bound := v[name]
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
// name reads as outside, because a hook runs nothing and cannot say where it leads, unless a variable the call
// assigned a static word, or a loop it runs over static words, holds it. A reader run through xargs reads what the
// listing before it names, which is inside only for a few listings (xargsReadsOutside). A directory the policy does
// not know reads as inside. A program that runs the code its words hold reads where that code names a place
// (codeReadsOutside), and one that makes a link reads where the link leads, which no hook sees.
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
	if slices.ContainsFunc(in.leaders, func(word shellWord) bool { return path.Base(word.text) == "xargs" }) {
		return p.xargsReadsOutside(in, at)
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

// xargsReadsOutside says what a reader run through xargs is handed may lie outside. It is inside only when xargs reads no
// argument file and the command that feeds it lists files inside that no link leads out of (listsInside).
func (p DenyPolicy) xargsReadsOutside(in readContext, at string) bool {
	for _, word := range in.leaders[slices.IndexFunc(in.leaders, func(word shellWord) bool { return path.Base(word.text) == "xargs" })+1:] {
		text := word.text
		if word.dynamic || text == "-a" || strings.HasPrefix(text, "--arg-file") ||
			strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") && strings.ContainsRune(text[1:], 'a') {
			return true
		}
	}
	return in.previous == nil || !p.listsInside(*in.previous, at, in.vars)
}

// listsInside says a command lists files that lie inside AskReadRoots, and no link: a find of files that follows no
// link, an fd of files, an rg that lists files or the files that match and follows no link (rg skips links by default),
// or a git listing of the tracked files of a worktree that holds no link that leads out of it.
func (p DenyPolicy) listsInside(command shellCommand, at string, vars shellVars) bool {
	words := command.words
	if len(words) == 0 || slices.ContainsFunc(words, func(word shellWord) bool { return word.dynamic || word.expands }) || len(command.reads)+len(command.writes) != 0 {
		return false
	}
	name, args := path.Base(words[0].text), words[1:]
	texts := make([]string, len(args))
	for i, word := range args {
		texts[i] = word.text
	}
	hasText := func(wanted ...string) bool {
		return slices.ContainsFunc(texts, func(text string) bool { return slices.Contains(wanted, text) })
	}
	hasLetter := func(letter rune) bool {
		return slices.ContainsFunc(texts, func(text string) bool {
			return strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") && strings.ContainsRune(text[1:], letter)
		})
	}
	rooted := func(paths []shellWord) bool {
		for _, word := range paths {
			if p.readsOutside(word, at, vars) {
				return false
			}
		}
		return len(paths) != 0 || at != "" && p.leadsUnder(at, at, at, p.AskReadRoots, true)
	}
	switch name {
	case "find":
		files := false
		for i, text := range texts {
			files = files || text == "-type" && i+1 < len(texts) && texts[i+1] == "f"
		}
		return files && !hasText("-L", "-follow", "-exec", "-execdir", "-ok", "-okdir") && rooted(findOperands(args))
	case "fd", "fdfind":
		files := hasText("--type=f", "-tf")
		for i, text := range texts {
			files = files || (text == "-t" || text == "--type") && i+1 < len(texts) && texts[i+1] == "f"
		}
		return files && !hasText("-L", "--follow") && !hasLetter('L') && rooted(listedPlaces(name, args))
	case "rg":
		lists := hasText("--files", "--files-with-matches") || hasLetter('l')
		return lists && !hasText("-L", "--follow") && !hasLetter('L') && rooted(listedPlaces(name, args))
	case "git":
		if !p.GitListingsInside || len(args) == 0 || strings.HasPrefix(args[0].text, "-") {
			return false
		}
		switch args[0].text {
		case "ls-files":
		case "diff":
			if !hasText("--name-only") {
				return false
			}
		case "grep":
			if !hasLetter('l') && !hasText("--files-with-matches") {
				return false
			}
		default:
			return false
		}
		return !hasText("-C", "--git-dir", "--work-tree", "-c") && !slices.ContainsFunc(texts, func(text string) bool {
			return strings.HasPrefix(text, "--git-dir=") || strings.HasPrefix(text, "--work-tree=")
		}) && at != "" && p.leadsUnder(at, at, at, p.AskReadRoots, true)
	}
	return false
}

// listedPlaces are the places a listing program lists, as its operands name them.
func listedPlaces(name string, args []shellWord) []shellWord {
	paths, _ := readOperands(name, readers[name], args)
	return paths
}

// codeReadsOutside says the code an interpreter is given names a place where AskReadRoots do not reach: inline code,
// as `python -c` takes it, or a here-document or here-string that feeds it. Code names a path as a word that begins
// with `/`, `~`, or `..`, which is as far as a hook reads a language it does not run. Code only a shell can write, or
// standard input a pipe feeds with nothing to read, asks, as the gates treat code they cannot read. A script file is
// the lane's own, and a module is no path.
func (p DenyPolicy) codeReadsOutside(name string, kind interpreter, args []shellWord, at string, command shellCommand, vars shellVars) bool {
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
func (p DenyPolicy) readsOutside(word shellWord, at string, vars shellVars) bool {
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
