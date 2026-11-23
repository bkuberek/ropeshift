package lanes

import (
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

// programReadsOutside says a program that reads files reads one where AskReadRoots do not reach, as far as its
// words say: a path it names, or the directory the session stands in when it names none. A path only a shell can
// name reads as outside, because a hook runs nothing and cannot say where it leads, and so does every path a reader
// run through xargs is handed. A directory the policy does not know reads as inside. A program that runs the code its
// words hold reads where that code names a place (codeReadsOutside).
func (p DenyPolicy) programReadsOutside(name string, args []shellWord, at string, command shellCommand, viaXargs bool) bool {
	if kind, runs := interpreterOf(name); runs && kind.letters != "" {
		return p.codeReadsOutside(kind, args, at, command)
	}
	rule, listed := readers[name]
	if !listed && name != "find" {
		return false
	}
	if viaXargs {
		return true
	}
	paths, recursive := readOperands(name, rule, args)
	for _, word := range paths {
		if p.readsOutside(word, at) {
			return true
		}
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

// codeReadsOutside says the code an interpreter is given names a place where AskReadRoots do not reach: inline code,
// as `python -c` takes it, or a here-document or here-string that feeds it. Code names a path as a word that begins
// with `/`, `~`, or `..`, which is as far as a hook reads a language it does not run. Code only a shell can write, or
// standard input a pipe feeds with nothing to read, asks, as the gates treat code they cannot read. A script file is
// the lane's own, and a module is no path.
func (p DenyPolicy) codeReadsOutside(kind interpreter, args []shellWord, at string, command shellCommand) bool {
	var code []shellWord
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
		if p.readsOutside(shellWord{text: word}, at) {
			return true
		}
	}
	return false
}

// readsOutside says a file a command reads, as the words give its name, lies where AskReadRoots do not reach. A name
// only a shell can expand reads as outside.
func (p DenyPolicy) readsOutside(word shellWord, at string) bool {
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
