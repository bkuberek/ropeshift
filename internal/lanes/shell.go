package lanes

import "strings"

// shellWord is one word of a command. Dynamic says it holds an expansion a hook cannot
// read, because a hook runs nothing, and glob that it holds an unquoted `~`, `*`, `?`, `[`,
// or `{`, which a shell may turn into other words.
type shellWord struct {
	text    string
	dynamic bool
	glob    bool
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
}

// maxShellDepth bounds how far a command inside a command is read. Past it a command is
// not understood, which reads as a denial.
const maxShellDepth = 8

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
	// heredocs are the delimiters whose bodies start at the next line.
	heredocs []string
}

type redirectKind int

const (
	redirectNone redirectKind = iota
	redirectWrite
	redirectRead
)

// parseShell splits a command line into simple commands and the substitutions in them.
func parseShell(text string) ([]shellCommand, []string) {
	p := &shellParser{text: text}
	for p.at < len(p.text) {
		p.step()
	}
	p.endCommand()
	return p.commands, p.nested
}

func (p *shellParser) endCommand() {
	if len(p.current.words) != 0 || len(p.current.writes) != 0 || p.current.comment {
		p.commands = append(p.commands, p.current)
	}
	p.current = shellCommand{}
	p.pending = redirectNone
}

func (p *shellParser) step() {
	switch p.text[p.at] {
	case ' ', '\t':
		p.at++
	case '\n':
		p.at++
		p.endCommand()
		p.skipHeredocs()
	case '#':
		p.current.comment = true
		if end := strings.IndexByte(p.text[p.at:], '\n'); end >= 0 {
			p.at += end
		} else {
			p.at = len(p.text)
		}
	case ';', '|', '(', ')':
		p.at++
		p.endCommand()
	case '&':
		if strings.HasPrefix(p.text[p.at:], "&>") {
			p.at += 2
			if p.at < len(p.text) && p.text[p.at] == '>' {
				p.at++
			}
			p.pending = redirectWrite
			return
		}
		p.at++
		p.endCommand()
	case '>':
		p.at++
		if p.at < len(p.text) && (p.text[p.at] == '>' || p.text[p.at] == '|') {
			p.at++
		}
		if p.at < len(p.text) && p.text[p.at] == '&' {
			p.at++
			p.pending = redirectRead
			return
		}
		p.pending = redirectWrite
	case '<':
		p.readInput()
	default:
		p.takeWord(p.readWord())
	}
}

// readInput takes `<`, `<<`, `<<-`, and `<<<`. None writes, and a here document's body is
// text and not commands.
func (p *shellParser) readInput() {
	switch {
	case strings.HasPrefix(p.text[p.at:], "<<<"):
		p.at += 3
		p.pending = redirectRead
	case strings.HasPrefix(p.text[p.at:], "<<"):
		p.at += 2
		if p.at < len(p.text) && p.text[p.at] == '-' {
			p.at++
		}
		for p.at < len(p.text) && (p.text[p.at] == ' ' || p.text[p.at] == '\t') {
			p.at++
		}
		if p.at < len(p.text) {
			p.heredocs = append(p.heredocs, p.readWord().text)
		}
		p.current.heredoc = true
	default:
		p.at++
		p.pending = redirectRead
	}
}

func (p *shellParser) skipHeredocs() {
	for _, delimiter := range p.heredocs {
		for p.at < len(p.text) {
			end := strings.IndexByte(p.text[p.at:], '\n')
			line := p.text[p.at:]
			if end >= 0 {
				line = p.text[p.at : p.at+end]
				p.at += end + 1
			} else {
				p.at = len(p.text)
			}
			if strings.TrimLeft(line, "\t") == delimiter {
				break
			}
		}
	}
	p.heredocs = nil
}

func (p *shellParser) takeWord(word shellWord) {
	switch p.pending {
	case redirectWrite:
		p.current.writes = append(p.current.writes, word)
		p.pending = redirectNone
		return
	case redirectRead:
		p.current.reads = append(p.current.reads, word)
		p.pending = redirectNone
		return
	}
	// A descriptor number written against its redirect belongs to the redirect.
	if p.at < len(p.text) && (p.text[p.at] == '>' || p.text[p.at] == '<') && isNumber(word.text) {
		return
	}
	p.current.words = append(p.current.words, word)
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
	for p.at < len(p.text) && !endsWord(p.text[p.at]) {
		switch c := p.text[p.at]; c {
		case '\\':
			p.at++
			if p.at < len(p.text) {
				out.WriteByte(p.text[p.at])
				p.at++
			}
		case '\'':
			p.at++
			end := strings.IndexByte(p.text[p.at:], '\'')
			if end < 0 {
				out.WriteString(p.text[p.at:])
				p.at = len(p.text)
				break
			}
			out.WriteString(p.text[p.at : p.at+end])
			p.at += end + 1
		case '"':
			p.at++
			p.readDoubleQuoted(&out, &word)
		case '$':
			p.readExpansion(&out, &word)
		case '`':
			p.at++
			p.readBackticks(&word)
		default:
			word.glob = word.glob || strings.IndexByte("~*?[{", c) >= 0
			out.WriteByte(c)
			p.at++
		}
	}
	word.text = out.String()
	return word
}

func (p *shellParser) readDoubleQuoted(out *strings.Builder, word *shellWord) {
	for p.at < len(p.text) {
		switch c := p.text[p.at]; c {
		case '"':
			p.at++
			return
		case '\\':
			p.at++
			if p.at < len(p.text) {
				out.WriteByte(p.text[p.at])
				p.at++
			}
		case '$':
			p.readExpansion(out, word)
		case '`':
			p.at++
			p.readBackticks(word)
		default:
			out.WriteByte(c)
			p.at++
		}
	}
}

func (p *shellParser) readBackticks(word *shellWord) {
	word.dynamic = true
	end := strings.IndexByte(p.text[p.at:], '`')
	if end < 0 {
		p.nested = append(p.nested, p.text[p.at:])
		p.at = len(p.text)
		return
	}
	p.nested = append(p.nested, p.text[p.at:p.at+end])
	p.at += end + 1
}

// readExpansion copies a variable as written, for a policy to name, and takes a
// substitution's text out to read in turn.
func (p *shellParser) readExpansion(out *strings.Builder, word *shellWord) {
	word.dynamic = true
	if strings.HasPrefix(p.text[p.at:], "$(") {
		p.at += 2
		start, depth := p.at, 1
		for p.at < len(p.text) && depth > 0 {
			switch p.text[p.at] {
			case '(':
				depth++
			case ')':
				depth--
			}
			p.at++
		}
		if depth == 0 {
			p.nested = append(p.nested, p.text[start:p.at-1])
		} else {
			p.nested = append(p.nested, p.text[start:])
		}
		return
	}
	out.WriteByte('$')
	p.at++
	if p.at < len(p.text) && p.text[p.at] == '{' {
		end := strings.IndexByte(p.text[p.at:], '}')
		if end >= 0 {
			out.WriteString(p.text[p.at : p.at+end+1])
			p.at += end + 1
			return
		}
	}
	for p.at < len(p.text) && isNameCharacter(p.text[p.at]) {
		out.WriteByte(p.text[p.at])
		p.at++
	}
}

func isNameCharacter(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
