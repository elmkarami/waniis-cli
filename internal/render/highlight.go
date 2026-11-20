package render

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/elmkarami/waniis-cli/internal/style"
)

// maxHighlight is how much of one code block is highlighted; the rest is shown plain.
const maxHighlight = 64 << 10

// highlighter colors a fenced code block line by line as it streams in. Each line is colored
// from the whole block so far, so strings and comments that span lines stay right. Colors are
// the terminal's own 16, so they follow its theme, light or dark.
type highlighter struct {
	lexer chroma.Lexer
	src   strings.Builder
	n     int // lines shown
	st    style.Style
}

func newHighlighter(lang string, st style.Style) *highlighter {
	h := &highlighter{st: st}
	if lang = strings.ToLower(strings.TrimSpace(lang)); lang != "" && st.On {
		if l := lexers.Get(lang); l != nil {
			h.lexer = chroma.Coalesce(l)
		}
	}
	return h
}

// line returns line colored.
func (h *highlighter) line(line string) string {
	idx := h.n
	h.n++
	if h.lexer == nil || h.src.Len()+len(line) > maxHighlight {
		h.lexer = nil
		return line
	}
	h.src.WriteString(line + "\n")
	it, err := h.lexer.Tokenise(nil, h.src.String())
	if err != nil {
		return line
	}
	lines := chroma.SplitTokensIntoLines(it.Tokens())
	if idx >= len(lines) {
		return line
	}
	var b, plain strings.Builder
	for _, tok := range lines[idx] {
		v := strings.TrimRight(tok.Value, "\n")
		plain.WriteString(v)
		if code := tokenCode(tok.Type); code != "" {
			b.WriteString(h.st.Code(code, v))
		} else {
			b.WriteString(v)
		}
	}
	if plain.String() != line {
		// The lexer saw the text differently (tabs, line ends): show it as written.
		return line
	}
	return b.String()
}

// tokenCode is the SGR code for a kind of token, "" for the terminal's own color.
func tokenCode(t chroma.TokenType) string {
	switch {
	case t.InCategory(chroma.Comment):
		return "2;3"
	case t == chroma.KeywordType:
		return "36"
	case t.InCategory(chroma.Keyword):
		return "35"
	case t.InSubCategory(chroma.LiteralString):
		return "32"
	case t.InSubCategory(chroma.LiteralNumber):
		return "33"
	case t == chroma.NameFunction, t == chroma.NameFunctionMagic:
		return "34"
	case t == chroma.NameBuiltin, t == chroma.NameBuiltinPseudo:
		return "36"
	case t == chroma.NameClass, t == chroma.NameAttribute, t == chroma.NameDecorator:
		return "33"
	case t == chroma.NameTag:
		return "35"
	case t == chroma.GenericInserted:
		return "32"
	case t == chroma.GenericDeleted:
		return "31"
	case t == chroma.GenericHeading, t == chroma.GenericSubheading:
		return "1"
	}
	return ""
}
