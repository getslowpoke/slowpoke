package detect

import "strings"

// directive is one nginx statement, with the statements of its block if it has one.
type directive struct {
	name  string
	args  []string
	block []directive
}

// parseNginx reads nginx configuration well enough to find servers, logs and locations:
// comments, quoted strings (which may hold ';' or '{') and nested blocks.
func parseNginx(src string) []directive {
	tokens := tokenize(src)
	out, _ := parseBlock(tokens, 0)
	return out
}

func parseBlock(tokens []string, i int) ([]directive, int) {
	var out []directive
	var words []string
	for i < len(tokens) {
		tok := tokens[i]
		i++
		switch tok {
		case ";":
			if len(words) > 0 {
				out = append(out, directive{name: words[0], args: words[1:]})
			}
			words = nil
		case "{":
			var block []directive
			block, i = parseBlock(tokens, i)
			if len(words) > 0 {
				out = append(out, directive{name: words[0], args: words[1:], block: block})
			}
			words = nil
		case "}":
			return out, i
		default:
			words = append(words, tok)
		}
	}
	return out, i
}

func tokenize(src string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			flush()
		case c == '"' || c == '\'':
			quote := c
			i++
			for i < len(src) && src[i] != quote {
				if src[i] == '\\' && i+1 < len(src) {
					i++
				}
				cur.WriteByte(src[i])
				i++
			}
			// A quoted string is always a token, even when empty.
			tokens = append(tokens, cur.String())
			cur.Reset()
		case c == ';' || c == '{' || c == '}':
			flush()
			tokens = append(tokens, string(c))
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return tokens
}

// walk visits every directive, with its depth below the starting block.
func walk(ds []directive, fn func(directive, int)) {
	var visit func([]directive, int)
	visit = func(ds []directive, depth int) {
		for _, d := range ds {
			fn(d, depth)
			visit(d.block, depth+1)
		}
	}
	visit(ds, 0)
}
