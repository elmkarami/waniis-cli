package commands

import (
	"strings"
	"testing"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/mark"
	"github.com/elmkarami/waniis-cli/internal/style"
)

func TestBannerPutsTheMarkBesideWhoAndWhere(t *testing.T) {
	lines := bannerLines(style.Style{}, bannerFacts{
		Version: "0.1.4", Model: "Claude Sonnet 5.5", Workspace: "Acme",
		Dir: "~/code/reeve", Repo: "elmkarami/reeve", Notices: []string{"A new version is available"},
	}, mark.Color16)
	text := strings.Join(lines, "\n")
	for _, want := range []string{"Waniis v0.1.4", "Claude Sonnet 5.5 · Acme", "~/code/reeve · elmkarami/reeve", "└ A new version is available"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	// Four lines of mark, each with something drawn, and the words in one column beside them.
	// Printed columns, not bytes: the blocks take three bytes each.
	colOf := func(line, word string) int { return style.Width(line[:strings.Index(line, word)]) }
	col := colOf(lines[0], "Waniis")
	for i, word := range []string{"Waniis", "Claude", "~"} {
		if !strings.ContainsAny(lines[i], "█▀▄") {
			t.Fatalf("line %d has no mark: %q", i, lines[i])
		}
		if colOf(lines[i], word) != col {
			t.Fatalf("misaligned: %q", lines[i])
		}
	}
	dev := strings.Join(bannerLines(style.Style{}, bannerFacts{Version: "dev"}, mark.Color16), "\n")
	if !strings.Contains(dev, "Waniis development build") {
		t.Fatal(dev)
	}
}

func TestWorkspaceLabel(t *testing.T) {
	me := &api.Me{}
	me.Workspaces = append(me.Workspaces,
		struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		}{"ws_p", "El Mehdi", "personal"},
		struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		}{"ws_a", "Acme", "team"})
	eq(t, workspaceLabel(me, "ws_a"), "Acme", "team")
	eq(t, workspaceLabel(me, "ws_p"), "personal workspace", "personal")
	eq(t, workspaceLabel(me, ""), "", "several and none chosen")
	eq(t, workspaceLabel(nil, ""), "", "unknown")
}

func TestFooterNamesTheMode(t *testing.T) {
	eq(t, footerFor(style.Style{}, "", ""), "►► auto mode (shift+tab to cycle)", "default")
	eq(t, footerFor(style.Style{}, "plan", "openai/gpt-5"), "►► plan mode (shift+tab to cycle) · openai/gpt-5", "chosen")
}

func TestLogoPixels(t *testing.T) {
	px := mark.Pixels(16, 8)
	dots := 0
	for _, row := range px {
		for _, ink := range row {
			if ink == mark.Dot {
				dots++
			}
		}
	}
	eq(t, dots, 4, "a 2×2 dot")
	if l := mark.Logo(16, 8, false, mark.Color16); len(l) != 4 {
		t.Fatalf("four lines: %q", l)
	}
}
