package sqlstore

import (
	"fmt"
	"strings"
)

// params binds a statement's `:name` parameters.
type params map[string]any

// compiled is a statement rewritten to the driver's positional form.
type compiled struct {
	text  string
	names []string
}

// compile rewrites `:name` parameters to positional ones. A repeated name reuses
// its position, and a colon inside a string literal or in a `::` cast is left
// alone.
func compile(text string, placeholder func(int) string) compiled {
	var out strings.Builder
	var names []string
	position := map[string]int{}
	inString := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\'':
			inString = !inString
			out.WriteByte(c)
		case inString || c != ':':
			out.WriteByte(c)
		case i+1 < len(text) && text[i+1] == ':':
			out.WriteString("::")
			i++
		case i+1 < len(text) && isNameStart(text[i+1]):
			end := i + 1
			for end < len(text) && isNamePart(text[end]) {
				end++
			}
			name := text[i+1 : end]
			at, seen := position[name]
			if !seen {
				names = append(names, name)
				at = len(names)
				position[name] = at
			}
			out.WriteString(placeholder(at))
			i = end - 1
		default:
			out.WriteByte(c)
		}
	}
	return compiled{text: out.String(), names: names}
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNamePart(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}

// bind orders the parameters. A missing name is a programming error that must
// not reach the database as a NULL.
func (c compiled) bind(p params) ([]any, error) {
	args := make([]any, len(c.names))
	for i, name := range c.names {
		value, ok := p[name]
		if !ok {
			return nil, fmt.Errorf("statement parameter %q is not bound", name)
		}
		args[i] = value
	}
	return args, nil
}
