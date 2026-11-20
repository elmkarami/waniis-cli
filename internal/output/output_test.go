package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestTableAligns(t *testing.T) {
	var b bytes.Buffer
	_ = Table(&b, []string{"ID", "NAME"}, [][]string{{"a", "x"}, {"longer-id", "y\nz"}})
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatal(lines)
	}
	if strings.Index(lines[0], "NAME") != strings.Index(lines[2], "y z") {
		t.Fatalf("misaligned:\n%s", b.String())
	}
}

func TestTruncate(t *testing.T) {
	if Truncate("héllo wörld", 5) != "héll…" || Truncate("short", 10) != "short" || Truncate("a\nb  c", 10) != "a b c" {
		t.Fatal("truncate")
	}
}

func TestJSONDoesNotEscapeHTML(t *testing.T) {
	var b bytes.Buffer
	_ = JSON(&b, map[string]string{"u": "a&b<c>"})
	if !strings.Contains(b.String(), "a&b<c>") {
		t.Fatal(b.String())
	}
}
