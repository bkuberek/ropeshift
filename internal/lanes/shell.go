package lanes

import (
	"slices"
	"strings"
)

// shellWord is one word of a command. Dynamic says it holds an expansion a hook cannot
// read, because a hook runs nothing, and glob that it holds an unquoted `~`, `*`, `?`, `[`,
// or `{`, which a shell may turn into other words.
type shellWord struct {
	text    string
	dynamic bool
	glob    bool
	// quoted says it holds a quote or an escape, so it is no keyword.
	quoted bool
	// expands says only a shell knows its value, which a lone `$` and a `$'...'` with no escape are not, and split that the
	// value may be more words or none. What follows tail in text stands as written.
	expands bool
	split   bool
	tail    int
	// process says it is the path of a process substitution.
	process bool
}

// shellCommand is one simple command, with the targets of its writing redirects and of its reading ones,
// whether a here-document feeds it, and whether a comment follows it. A comment on a line of its own is a
// command with no words.
type shellCommand struct {
	words   []shellWord
	writes  []shellWord
	reads   []shellWord
	heredoc bool
	comment bool
	// fed is what its here-documents and here-strings give it, and piped says a pipe feeds it.
	fed   []fedText
	piped bool
	// processes are the texts of its process substitutions, which run where it stands.
	processes []string
}

// fedText is what a here-document or a here-string gives a command, as the shell passes it on where it can be read.
type fedText struct {
	text    string
	expands bool
}

// hereDocument is a here-document whose body starts at the next line, and the command it feeds.
type hereDocument struct {
	delimiter string
	// literal says the delimiter was quoted, so the body expands nothing, and tabs that `<<-` strips leading tabs.
	literal bool
	tabs    bool
	owner   int
}

// A here-document's owner until its command ends, and when that command has nothing to feed.
const (
	ownerPending = -1
	ownerNone    = -2
)

// maxShellDepth bounds how far a command inside a command is read. Past it a command is
// not understood, which reads as a denial.
const maxShellDepth = 8

// maxShellNesting bounds how deep one reading follows substitutions and expansions, and maxShellTries how often it
// tries a `((` as arithmetic and goes back. Past either, a text is not understood.
const (
	maxShellNesting = 64
	maxShellTries   = 256
)

// maxJoinedLine bounds the continued lines read as one in a here-document.
const maxJoinedLine = 1 << 16

// processPath stands for the path a shell gives a process substitution.
const processPath = "/dev/fd/63"

// placeNames and placeCommands give a directory that a word begins with, one word however it expands.
var (
	placeNames    = []string{"HOME", "PWD", "GOPATH", "GOBIN"}
	placeCommands = []string{"pwd", "go env GOPATH", "go env GOBIN", "git rev-parse --show-toplevel"}
)

// shellParser reads a command line the way a shell splits it, and no further. It is not a
// shell: it holds to what a policy needs, and a guardrail is not a sandbox.
type shellParser struct {
	text     string
	at       int
	commands []shellCommand
	current  shellCommand
	// nested holds the text of each substitution, for a policy to read in turn.
	nested []string
	// pending is a redirect waiting for its target.
	pending redirectKind
	// heredocs are those whose bodies start at the next line.
	heredocs []hereDocument
	// unread says the text holds what this reading cannot follow, such as a quote that never closes.
	unread bool
	// substitution says this reads the inside of a substitution, which closed ends at a `)` no parens opened.
	substitution bool
	closed       bool
	parens       int
	// cases counts the open `case` statements. Pattern says a pattern is next, opened that a `(` leads it, and test
	// that the text stands inside `[[ ]]`, where an operator is no operator of the shell.
	cases   int
	pattern bool
	opened  bool
	test    bool
	// piped says a pipe feeds the next command.
	piped bool
	// nesting and tries hold the reading to maxShellNesting and maxShellTries.
	nesting int
	tries   int
	// wordEnd is where the last word ended, which says whether a `(` stands against it.
	wordEnd int
	// places says the text gives placeNames and placeCommands no other meaning.
	places bool
}

type redirectKind int

const (
	redirectNone redirectKind = iota
	redirectWrite
	redirectRead
	redirectHere
)

// parseShell splits a command line into simple commands and the substitutions in them.
func parseShell(text string) ([]shellCommand, []string) {
	p := parseScript(text)
	return p.commands, p.nested
}

// parseScript is parseShell, and it says whether it could follow the whole text.
func parseScript(text string) *shellParser {
	p := &shellParser{text: text, places: true, unread: strings.ContainsRune(text, 0)}
	for _, change := range []string{"()", "function ", "read ", "HOME=", "PWD=", "GOPATH=", "GOBIN="} {
		p.places = p.places && !strings.Contains(text, change)
	}
	p.run()
	return p
}

func (p *shellParser) run() {
	for p.at < len(p.text) && !p.closed {
		p.step()
	}
	p.endCommand()
	if p.test || (p.substitution && (!p.closed || len(p.heredocs) != 0)) {
		p.unread = true
	}
}

func (p *shellParser) endCommand() {
	owner := ownerNone
	if len(p.current.words) != 0 || len(p.current.writes) != 0 || p.current.comment {
		owner = len(p.commands)
		if len(p.current.words) != 0 {
			p.current.piped, p.piped = p.piped, false
		}
		p.commands = append(p.commands, p.current)
	}
	for i := range p.heredocs {
		if p.heredocs[i].owner == ownerPending {
			p.heredocs[i].owner = owner
		}
	}
	p.current = shellCommand{}
	p.pending = redirectNone
}

// next passes over continued lines and returns the byte after them, which is how a shell reads an operator.
func (p *shellParser) next() byte {
	for strings.HasPrefix(p.text[p.at:], "\\\n") {
		p.at += 2
	}
	return p.peek()
}

func (p *shellParser) peek() byte {
	if p.at < len(p.text) {
		return p.text[p.at]
	}
	return 0
}

func (p *shellParser) step() {
	switch c := p.text[p.at]; {
	case c == ' ' || c == '\t':
		p.at++
	case strings.HasPrefix(p.text[p.at:], "\\\n"):
		p.at += 2
	case c == '\n':
		p.at++
		p.endLine()
	case c == '#':
		p.current.comment = true
		p.skipLine()
	case strings.HasPrefix(p.text[p.at:], "<(") || strings.HasPrefix(p.text[p.at:], ">("):
		p.takeWord(p.readWord())
	case endsWord(c):
		against := p.at == p.wordEnd
		p.at++
		if !p.test {
			p.readOperator(c, against)
		}
	default:
		p.takeWord(p.readWord())
		p.wordEnd = p.at
	}
}

func (p *shellParser) skipLine() {
	if end := strings.IndexByte(p.text[p.at:], '\n'); end >= 0 {
		p.at += end
	} else {
		p.at = len(p.text)
	}
}

func (p *shellParser) endLine() {
	if p.test {
		return
	}
	// `case WORD` takes its `in` from a later line too.
	words := p.current.words
	if rest := words[min(commandStart(words), len(words)):]; len(rest) == 2 && isKeyword(rest[0], "case") {
		p.skipHeredocs()
		return
	}
	if len(p.current.words) != 0 {
		p.pattern = false
	}
	p.endCommand()
	p.skipHeredocs()
}

func (p *shellParser) readOperator(c byte, against bool) {
	next := p.next()
	switch c {
	case ';':
		p.pattern = false
		p.endCommand()
		if p.cases > 0 && (next == ';' || next == '&' || next == '|') {
			p.at++
			if p.next() == '&' {
				p.at++
			}
			p.pattern, p.opened = true, false
		}
	case '&':
		if next == '>' {
			p.at++
			if p.next() == '>' {
				p.at++
			}
			p.pending = redirectWrite
			return
		}
		// zsh ends a command it disowns with `&!` or `&|`.
		if next == '&' || next == '!' || next == '|' {
			p.at++
		}
		p.pattern = false
		p.endCommand()
	case '|':
		if p.pattern {
			p.current = shellCommand{}
			return
		}
		p.endCommand()
		p.piped = next != '|'
		if next == '|' || next == '&' {
			p.at++
		}
	case '(':
		p.openParen(next, against)
	case ')':
		if p.pattern {
			p.current, p.pattern, p.opened = shellCommand{}, false, false
			return
		}
		p.endCommand()
		if p.parens > 0 {
			p.parens--
		} else if p.substitution {
			p.closed = true
		}
	case '>':
		p.pending = redirectWrite
		if next == '>' || next == '|' {
			p.at++
		}
		// zsh reads `>!` as `>|`, where bash takes a file's name.
		p.unread = p.unread || p.peek() == '!'
		if p.next() == '&' {
			p.at++
			p.readDescriptor()
		}
	case '<':
		p.readInput(next)
	}
}

func (p *shellParser) openParen(next byte, against bool) {
	if p.pattern {
		p.current, p.opened = shellCommand{}, true
		return
	}
	words := p.current.words
	if next == '(' && p.startsCommand(words) {
		mark := p.at
		p.at++
		if p.readArithmetic(')') {
			p.current.words = append(words, shellWord{text: "((", dynamic: true})
			return
		}
		p.at = mark
	}
	if last := len(words) - 1; last >= 0 && against && !words[last].quoted {
		if name, assigns := strings.CutSuffix(words[last].text, "="); assigns && assignedName(name) {
			p.readArray()
			words[last].dynamic = true
			return
		}
	}
	// Against a word, a `(` defines a function with its `)`. Any other is a pattern of zsh or of bash's extglob,
	// inside which a `#` starts no comment.
	if against && next != ')' && len(words) != 0 && !isKeyword(words[len(words)-1], commandKeywords...) {
		p.unread = true
	}
	p.endCommand()
	p.parens++
}

// startsCommand says a command, and no argument, comes after these words, with no redirect before it, which is the
// one place a shell reads a keyword or a `((`.
func (p *shellParser) startsCommand(words []shellWord) bool {
	if len(p.current.writes) != 0 || len(p.current.reads) != 0 || p.current.heredoc {
		return false
	}
	start := commandStart(words)
	return start >= len(words) || (len(words) == start+1 && isKeyword(words[start], "for"))
}

// commandKeywords come before a command.
var commandKeywords = append(slices.Clone(shellKeywords), "esac", "time")

// commandStart is where the command after the keywords of words begins.
func commandStart(words []shellWord) int {
	at := 0
	for at < len(words) {
		switch {
		case isKeyword(words[at], "function"):
			at += 2
		case isKeyword(words[at], "coproc"), isKeyword(words[at], commandKeywords...):
			at++
		case words[at].text == "-p" && at > 0 && isKeyword(words[at-1], "time"):
			at++
		default:
			return at
		}
	}
	return at
}

func isKeyword(word shellWord, keywords ...string) bool {
	return !word.quoted && !word.dynamic && slices.Contains(keywords, word.text)
}

// assignedName says text is what stands before the `=` of an assignment: a name, with a subscript or a `+`.
func assignedName(text string) bool {
	text = strings.TrimSuffix(text, "+")
	if open := strings.IndexByte(text, '['); open > 0 && strings.HasSuffix(text, "]") {
		text = text[:open]
	}
	return isAssignment(text + "=")
}

// readArray reads the values of `name=(...)`, which are words and no commands.
func (p *shellParser) readArray() {
	for p.at < len(p.text) {
		switch c := p.text[p.at]; {
		case c == ' ' || c == '\t' || c == '\n':
			p.at++
		case strings.HasPrefix(p.text[p.at:], "\\\n"):
			p.at += 2
		case c == '#':
			p.skipLine()
		case c == ')':
			p.at++
			// Bash and zsh differ on a word written against the `)`.
			if next := p.next(); p.at < len(p.text) && !endsWord(next) {
				p.unread = true
			}
			return
		case strings.HasPrefix(p.text[p.at:], "<(") || strings.HasPrefix(p.text[p.at:], ">("), !endsWord(c):
			p.readWord()
		default:
			p.unread = true
			return
		}
	}
	p.unread = true
}

// readInput takes `<`, `<>`, `<&`, `<<`, `<<-`, and `<<<`. Only `<>` writes, and a here document's body is
// text and not commands.
func (p *shellParser) readInput(next byte) {
	switch next {
	case '>':
		p.at++
		p.pending = redirectWrite
	case '&':
		p.at++
		p.readDescriptor()
	case '<':
		p.at++
		third := p.next()
		if third == '<' {
			p.at++
			p.pending = redirectHere
			return
		}
		document := hereDocument{tabs: third == '-', owner: ownerPending}
		if document.tabs {
			p.at++
		}
		for p.next() == ' ' || p.peek() == '\t' {
			p.at++
		}
		p.current.heredoc = true
		if p.at >= len(p.text) || endsWord(p.peek()) {
			p.unread = true
			return
		}
		delimiter := p.readWord()
		// A `]` or a `)` in an unquoted delimiter is more likely arithmetic, which takes no here-document.
		p.unread = p.unread || delimiter.expands || (!delimiter.quoted && strings.ContainsAny(delimiter.text, "])"))
		document.delimiter, document.literal = delimiter.text, delimiter.quoted
		p.heredocs = append(p.heredocs, document)
	default:
		p.pending = redirectRead
	}
}

// readDescriptor reads what follows `<&` or `>&`. A `-` there is the whole target, and a word against it is a word
// of the command.
func (p *shellParser) readDescriptor() {
	for p.next() == ' ' || p.peek() == '\t' {
		p.at++
	}
	p.pending = redirectRead
	if p.peek() == '-' {
		p.at++
		p.takeWord(shellWord{text: "-"})
	}
}

func (p *shellParser) skipHeredocs() {
	for _, document := range p.heredocs {
		p.readDocument(document)
	}
	p.heredocs = nil
}

// readDocument reads one here-document's body, and the substitutions in it where its delimiter was not quoted.
func (p *shellParser) readDocument(document hereDocument) {
	start, end, ended := p.at, p.at, false
	for p.at < len(p.text) {
		began := p.at
		line := p.readLine()
		for !document.literal && p.at < len(p.text) && len(line) < maxJoinedLine && (len(line)-len(strings.TrimRight(line, `\`)))%2 == 1 {
			line = line[:len(line)-1] + p.readLine()
		}
		stripped := line
		if document.tabs {
			stripped = strings.TrimLeft(line, "\t")
		}
		if stripped == document.delimiter {
			end, ended = began, true
			break
		}
		// A substitution may close on the delimiter's own line.
		if p.substitution && strings.HasPrefix(stripped, document.delimiter+")") {
			end, ended = began, true
			p.at = min(began+len(line)-len(stripped)+len(document.delimiter), len(p.text))
			break
		}
		end = p.at
	}
	// A body that never ends may be commands this misread as one.
	p.unread = p.unread || !ended
	body := fedText{text: p.text[start:max(start, end)]}
	if !document.literal {
		body = p.expandDocument(body.text)
	}
	if document.owner >= 0 && document.owner < len(p.commands) {
		p.commands[document.owner].fed = append(p.commands[document.owner].fed, body)
	}
}

func (p *shellParser) readLine() string {
	start := p.at
	if end := strings.IndexByte(p.text[p.at:], '\n'); end >= 0 {
		p.at += end + 1
		return p.text[start : p.at-1]
	}
	p.at = len(p.text)
	return p.text[start:]
}

// expandDocument reads a here-document's body as a shell does before it feeds it on: escapes, and expansions.
func (p *shellParser) expandDocument(text string) fedText {
	body := &shellParser{text: text, places: p.places, nesting: p.nesting, tries: p.tries}
	var out strings.Builder
	var word shellWord
	for body.at < len(text) {
		switch c := text[body.at]; c {
		case '\\':
			body.readEscape(&out, "$`\\\n")
		case '$':
			body.readExpansion(&out, &word, true)
		case '`':
			body.at++
			body.readBackticks(&out, &word, true)
		default:
			out.WriteByte(c)
			body.at++
		}
	}
	p.nested = append(p.nested, body.nested...)
	p.unread, p.tries = p.unread || body.unread, body.tries
	return fedText{text: out.String(), expands: word.expands}
}

func (p *shellParser) takeWord(word shellWord) {
	pending := p.pending
	p.pending = redirectNone
	switch pending {
	case redirectWrite:
		// A process substitution is a pipe, and no file.
		if !word.process {
			p.current.writes = append(p.current.writes, word)
		}
		return
	case redirectRead:
		p.current.reads = append(p.current.reads, word)
		return
	case redirectHere:
		p.current.reads = append(p.current.reads, word)
		p.current.fed = append(p.current.fed, fedText{text: word.text, expands: word.expands})
		return
	}
	// A descriptor number written against its redirect belongs to the redirect.
	if p.at < len(p.text) && (p.text[p.at] == '>' || p.text[p.at] == '<') && !word.quoted && isNumber(word.text) &&
		!strings.HasPrefix(p.text[p.at:], "<(") && !strings.HasPrefix(p.text[p.at:], ">(") {
		return
	}
	before := p.current.words
	p.current.words = append(before, word)
	p.readKeyword(before, word)
}

// readKeyword follows the keywords that change how a shell reads what comes after them.
func (p *shellParser) readKeyword(before []shellWord, word shellWord) {
	switch {
	case p.test:
		p.test = !isKeyword(word, "]]")
		return
	case p.pattern:
		if len(before) == 0 && !p.opened && isKeyword(word, "esac") {
			p.pattern = false
			p.cases = max(p.cases-1, 0)
		}
		return
	case !isKeyword(word, "esac", "[[", "in", "do"):
		return
	}
	if len(p.current.writes) != 0 || len(p.current.reads) != 0 || p.current.heredoc {
		return
	}
	start := commandStart(before)
	head := before[min(start, len(before)):]
	switch {
	case len(head) == 0 && word.text == "esac":
		p.cases = max(p.cases-1, 0)
	case len(head) == 0 && word.text == "[[":
		p.test = true
	case len(head) == 2 && word.text == "in" && isKeyword(head[0], "case"):
		p.cases++
		p.endCommand()
		p.pattern, p.opened = true, false
	case len(head) == 2 && word.text == "do" && isKeyword(head[0], "for", "select"):
		p.endCommand()
	}
}

func isNumber(text string) bool {
	if text == "" {
		return false
	}
	for _, c := range []byte(text) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func endsWord(c byte) bool {
	return strings.IndexByte(" \t\n;&|()<>", c) >= 0
}

func (p *shellParser) readWord() shellWord {
	var out strings.Builder
	word := shellWord{}
	brackets := 0
	for p.at < len(p.text) {
		c := p.text[p.at]
		if strings.HasPrefix(p.text[p.at:], "<(") || strings.HasPrefix(p.text[p.at:], ">(") {
			p.at += 2
			inner := p.readSubstitution()
			p.current.processes = append(p.current.processes, inner)
			out.WriteString(processPath)
			word.process, word.expands, word.tail = true, true, out.Len()
			continue
		}
		if endsWord(c) {
			break
		}
		switch c {
		case '\\':
			p.at++
			if p.at < len(p.text) {
				if next := p.text[p.at]; next != '\n' {
					out.WriteByte(next)
					word.quoted = true
				}
				p.at++
			}
		case '\'':
			word.quoted = true
			p.readSingleQuoted(&out)
		case '"':
			word.quoted = true
			p.at++
			p.readDoubleQuoted(&out, &word)
		case '$':
			p.readExpansion(&out, &word, false)
		case '`':
			p.at++
			p.readBackticks(&out, &word, false)
		default:
			switch {
			case c == ']' && brackets > 0:
				brackets--
			case c == '[' && (brackets > 0 || (!word.quoted && !word.expands && isAssignment(out.String()+"="))):
				brackets++
			}
			word.glob = word.glob || strings.IndexByte("~*?[{", c) >= 0
			out.WriteByte(c)
			p.at++
		}
	}
	// Before a command, a shell reads `name[` on to its `]` as a subscript, past blanks, operators, and lines.
	if brackets > 0 && !slices.ContainsFunc(p.current.words, func(before shellWord) bool {
		return !isAssignment(before.text) && !isKeyword(before, commandKeywords...)
	}) {
		p.unread = true
	}
	word.text = out.String()
	return word
}

func (p *shellParser) readSingleQuoted(out *strings.Builder) {
	p.at++
	end := strings.IndexByte(p.text[p.at:], '\'')
	if end < 0 {
		p.unread = true
		out.WriteString(p.text[p.at:])
		p.at = len(p.text)
		return
	}
	out.WriteString(p.text[p.at : p.at+end])
	p.at += end + 1
}

// readEscape takes a backslash, which escapes only these bytes where this is read and stands as itself before others.
func (p *shellParser) readEscape(out *strings.Builder, escaped string) {
	p.at++
	if p.at < len(p.text) && strings.IndexByte(escaped, p.text[p.at]) >= 0 {
		if p.text[p.at] != '\n' {
			out.WriteByte(p.text[p.at])
		}
		p.at++
		return
	}
	out.WriteByte('\\')
}

func (p *shellParser) readDoubleQuoted(out *strings.Builder, word *shellWord) {
	for p.at < len(p.text) {
		switch c := p.text[p.at]; c {
		case '"':
			p.at++
			return
		case '\\':
			p.readEscape(out, "$`\"\\\n")
		case '$':
			p.readExpansion(out, word, true)
		case '`':
			p.at++
			p.readBackticks(out, word, true)
		default:
			out.WriteByte(c)
			p.at++
		}
	}
	p.unread = true
}

func (p *shellParser) readBackticks(out *strings.Builder, word *shellWord, quoted bool) {
	word.dynamic = true
	escaped := "$`\\"
	if quoted {
		escaped += `"`
	}
	var inner strings.Builder
	closed := false
	for p.at < len(p.text) && !closed {
		switch c := p.text[p.at]; c {
		case '`':
			p.at++
			closed = true
		case '\\':
			p.readEscape(&inner, escaped)
		default:
			inner.WriteByte(c)
			p.at++
		}
	}
	p.unread = p.unread || !closed
	p.nested = append(p.nested, inner.String())
	word.expanded(out.Len(), !quoted)
}

// expanded marks an expansion that ends at tail.
func (w *shellWord) expanded(tail int, split bool) {
	w.expands, w.split, w.tail = true, w.split || split, tail
}

// readSubstitution takes out the text up to the `)` that closes it, reading quotes, comments, and here-documents on
// the way as a shell does.
func (p *shellParser) readSubstitution() string {
	start := p.at
	if p.nesting >= maxShellNesting {
		p.unread, p.at = true, len(p.text)
		return p.text[start:]
	}
	inner := &shellParser{text: p.text, at: start, substitution: true, nesting: p.nesting + 1, tries: p.tries, places: p.places}
	inner.run()
	p.at, p.tries, p.unread = inner.at, inner.tries, p.unread || inner.unread
	end := p.at
	if inner.closed {
		end--
	}
	text := p.text[start:end]
	p.nested = append(p.nested, text)
	return text
}

// readExpansion copies a variable as written, for a policy to name, and takes a
// substitution's text out to read in turn.
func (p *shellParser) readExpansion(out *strings.Builder, word *shellWord, quoted bool) {
	word.dynamic = true
	p.at++
	switch next := p.next(); {
	case next == '(':
		p.at++
		if p.next() == '(' {
			mark := p.at
			p.at++
			if p.readArithmetic(')') {
				word.expanded(out.Len(), false)
				return
			}
			p.at = mark
		}
		inner := strings.TrimSpace(p.readSubstitution())
		word.expanded(out.Len(), !quoted && (!p.places || !slices.Contains(placeCommands, inner)))
	case next == '[':
		p.at++
		p.unread = !p.readArithmetic(']') || p.unread
		word.expanded(out.Len(), false)
	case next == '{':
		start := p.at
		p.at++
		p.readParameter(quoted)
		raw := p.text[start:p.at]
		out.WriteString("$" + raw)
		word.expanded(out.Len(), p.splits(strings.TrimSuffix(raw[1:], "}"), quoted))
	case next == '\'' && !quoted:
		word.quoted = true
		p.readEscaped(out, word)
	case next == '"' && !quoted:
		word.quoted = true
		p.at++
		p.readDoubleQuoted(out, word)
	default:
		p.readVariable(out, word, quoted)
	}
}

func (p *shellParser) readVariable(out *strings.Builder, word *shellWord, quoted bool) {
	start := p.at
	for p.at < len(p.text) && isNameCharacter(p.text[p.at]) {
		p.at++
	}
	if p.at == start {
		switch c := p.peek(); {
		case c != 0 && strings.IndexByte("@*#?$!-", c) >= 0:
			p.at++
		case c != 0 && strings.IndexByte("=~^+", c) >= 0 && p.at+1 < len(p.text) && isNameCharacter(p.text[p.at+1]):
			// zsh takes these between a `$` and the name.
			for p.at++; p.at < len(p.text) && isNameCharacter(p.text[p.at]); p.at++ {
			}
		default:
			out.WriteByte('$')
			return
		}
	}
	name := p.text[start:p.at]
	out.WriteString("$" + name)
	word.expanded(out.Len(), p.splits(name, quoted))
}

// splits says an expansion of this name may give other than one word.
func (p *shellParser) splits(name string, quoted bool) bool {
	switch {
	case name == "@", strings.Contains(name, "[@]"), strings.HasPrefix(name, "@"), strings.HasPrefix(name, "="):
		return true
	case quoted, slices.Contains([]string{"#", "?", "$", "!"}, name):
		return false
	}
	return !p.places || !slices.Contains(placeNames, name)
}

// readParameter reads `${...}` to the brace that closes it, and what expands inside it.
func (p *shellParser) readParameter(quoted bool) {
	// Bash runs the body of `${ ...; }` and `${|...}` as commands.
	if next := p.peek(); p.nesting >= maxShellNesting || next != 0 && strings.IndexByte(" \t\n|", next) >= 0 {
		p.unread, p.at = true, len(p.text)
		return
	}
	p.nesting++
	defer func() { p.nesting-- }()
	// A quote counts in a pattern after `#`, `%`, `/`, `^`, or `,`, inside double quotes too.
	operator := p.at + 1
	for operator < len(p.text) && (isNameCharacter(p.text[operator]) || p.text[operator] == '[' || p.text[operator] == ']') {
		operator++
	}
	quotes := !quoted || (operator < len(p.text) && strings.IndexByte("#%/^,", p.text[operator]) >= 0)
	var skipped strings.Builder
	var word shellWord
	for p.at < len(p.text) {
		switch c := p.text[p.at]; {
		case c == '}':
			p.at++
			return
		case c == '\\':
			p.at = min(p.at+2, len(p.text))
		case c == '\'' && quotes:
			p.readSingleQuoted(&skipped)
		case c == '"':
			p.at++
			p.readDoubleQuoted(&skipped, &word)
		case c == '$':
			p.readExpansion(&skipped, &word, quoted)
		case c == '`':
			p.at++
			p.readBackticks(&skipped, &word, quoted)
		case !quoted && (strings.HasPrefix(p.text[p.at:], "<(") || strings.HasPrefix(p.text[p.at:], ">(")):
			p.at += 2
			p.readSubstitution()
		default:
			p.at++
		}
	}
	p.unread = true
}

// readArithmetic reads to the `))` or the `]` that ends arithmetic, and what expands inside it. It says false where
// none does, as in a `((` that opens two subshells, and the caller goes back.
func (p *shellParser) readArithmetic(end byte) bool {
	if p.nesting >= maxShellNesting || p.tries >= maxShellTries {
		p.unread = true
		return false
	}
	p.tries++
	p.nesting++
	defer func() { p.nesting-- }()
	open := byte('(')
	if end == ']' {
		open = '['
	}
	found, unread, depth := len(p.nested), p.unread, 0
	var skipped strings.Builder
	var word shellWord
read:
	for p.at < len(p.text) {
		switch c := p.text[p.at]; c {
		case '\\':
			p.at = min(p.at+2, len(p.text))
		case '\'':
			p.readSingleQuoted(&skipped)
		case '"':
			p.at++
			p.readDoubleQuoted(&skipped, &word)
		case '$':
			p.readExpansion(&skipped, &word, true)
		case '`':
			p.at++
			p.readBackticks(&skipped, &word, true)
		default:
			p.at++
			switch {
			case c == open:
				depth++
			case c != end:
			case depth > 0:
				depth--
			case end == ']':
				return true
			case p.next() == ')':
				p.at++
				return true
			default:
				break read
			}
		}
	}
	p.nested, p.unread = p.nested[:found], unread
	return false
}

// readEscaped reads `$'...'`. One with an escape has a value this does not decode.
func (p *shellParser) readEscaped(out *strings.Builder, word *shellWord) {
	p.at++
	start, escaped := p.at, false
	for p.at < len(p.text) {
		switch p.text[p.at] {
		case '\'':
			out.WriteString(p.text[start:p.at])
			p.at++
			if escaped {
				word.expanded(out.Len(), false)
			}
			return
		case '\\':
			escaped = true
			p.at = min(p.at+2, len(p.text))
		default:
			p.at++
		}
	}
	p.unread = true
}

func isNameCharacter(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
