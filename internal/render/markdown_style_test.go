package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/elmkarami/waniis-cli/internal/style"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

func styled(text string, chunk int) string {
	var b strings.Builder
	md := NewMD(&b, MDOptions{Style: style.Style{On: true}, Width: 60})
	if chunk <= 0 {
		md.Write(text)
	} else {
		for i := 0; i < len(text); i += chunk {
			md.Write(text[i:min(len(text), i+chunk)])
		}
	}
	md.Flush()
	return b.String()
}

const sample = "# Title\n\n**GitHub:** @sara, *personal* workspace, see `apps/api` and [docs](https://x.test/d).\n\n" +
	"## Next\n\n- one **bold** item\n- [ ] open task\n- [x] done task\n  * nested\n1. first\n> quoted `code`\n\n---\n\n" +
	"```go\n// Hello says hi\nfunc Hello() int { return 42 }\n```\n\n| Repo | **Stars** |\n|---|---|\n| `acme/api` | 12 |\n\n" +
	"snake_case_name, 2 * 3 * 4, \\*not italic\\* and ~~struck~~ text.\n***both*** and __strong__ and _em_.\n"

func TestMarkdownStyles(t *testing.T) {
	out := styled(sample, 0)
	text := plain(out)
	for _, want := range []string{
		"Title\n", "GitHub: @sara, personal workspace, see apps/api and docs (https://x.test/d).",
		"• one bold item", "☐ open task", "☑ done task", "  • nested", "1. first", "│ quoted code",
		strings.Repeat("─", 40), "// Hello says hi", "func Hello() int { return 42 }",
		"snake_case_name, 2 * 3 * 4, *not italic* and struck text.", "both and strong and em.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	for _, gone := range []string{"**", "# Title", "](", "- one", "~~"} {
		if strings.Contains(text, gone) {
			t.Errorf("marker %q left in:\n%s", gone, text)
		}
	}
	for _, want := range []string{
		"\x1b[0m\x1b[1;4mTitle",   // h1: bold, underlined
		"\x1b[0m\x1b[1mGitHub:",   // bold
		"\x1b[0m\x1b[3mpersonal",  // italic
		"\x1b[0m\x1b[36mapps/api", // inline code
		"\x1b[4mdocs",             // link text
		"\x1b[9mstruck",           // strikethrough
		"\x1b[35mfunc\x1b[0m",     // a keyword
		"\x1b[2;3m// Hello",       // a comment
		"\x1b[33m42\x1b[0m",       // a number
		"\x1b[2m```go\x1b[0m",     // the fence, dim
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

// However the answer is chunked as it streams, it reads the same as all at once.
func TestMarkdownStylesStreamTheSame(t *testing.T) {
	whole := styled(sample, 0)
	for _, chunk := range []int{1, 2, 3, 5, 7, 13} {
		if got := styled(sample, chunk); got != whole {
			t.Fatalf("chunks of %d differ:\n got %q\nwant %q", chunk, got, whole)
		}
	}
}

// Ordinary words show as soon as their line's start is known, not at the end of the line.
func TestMarkdownStylesStreamLive(t *testing.T) {
	var b strings.Builder
	md := NewMD(&b, MDOptions{Style: style.Style{On: true}, Width: 60})
	md.Write("Looking at the **flaky")
	if got := plain(b.String()); got != "Looking at the flaky" {
		t.Fatalf("got %q", got)
	}
	md.Write("** test now *")
	if got := plain(b.String()); got != "Looking at the flaky test now " {
		t.Fatalf("a lone marker at the end waits for what follows: got %q", got)
	}
	md.Write("\n")
	if got := plain(b.String()); got != "Looking at the flaky test now *\n" {
		t.Fatalf("got %q", got)
	}
	md.Write("Then")
	md.Flush()
	if got := plain(b.String()); got != "Looking at the flaky test now *\nThen\n" {
		t.Fatalf("got %q", got)
	}
}

// Without colors the Markdown is shown exactly as written (pipes, files, NO_COLOR).
func TestMarkdownWithoutColorsIsUntouched(t *testing.T) {
	var b strings.Builder
	md := NewMD(&b, MDOptions{Style: style.Style{On: false}, Width: 60})
	text := "# T\n\n**b** *i* `c`\n\n```go\nfunc x() {}\n```\n"
	md.Write(text)
	md.Flush()
	if b.String() != text {
		t.Fatalf("got %q", b.String())
	}
}

func TestUnknownLanguageIsPlain(t *testing.T) {
	out := styled("```nosuchlang\nlet x = 1\n```\n", 0)
	if !strings.Contains(out, "\nlet x = 1\n") {
		t.Fatalf("got %q", out)
	}
}

// A block comment that spans lines stays a comment on every line.
func TestHighlightKeepsStateAcrossLines(t *testing.T) {
	out := styled("```go\n/* one\ntwo */\nx := 1\n```\n", 1)
	if !strings.Contains(out, "\x1b[2;3mtwo */\x1b[0m") {
		t.Fatalf("got %q", out)
	}
}

func TestDiffBlock(t *testing.T) {
	out := styled("```diff\n-old\n+new\n```\n", 0)
	if !strings.Contains(out, "\x1b[31m-old") || !strings.Contains(out, "\x1b[32m+new") {
		t.Fatalf("got %q", out)
	}
}
