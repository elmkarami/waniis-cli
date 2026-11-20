package chat

import (
	"strings"
	"testing"
)

func testPicker() *Picker {
	return &Picker{
		Tabs: []PickTab{
			{Name: "All"},
			{Name: "Configured", Show: func(it PickItem) bool { return !it.Disabled }},
			{Name: "Not configured", Show: func(it PickItem) bool { return it.Disabled }},
			{Name: "Search", Search: true},
		},
		Items: []PickItem{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta", Current: true}, {ID: "g", Label: "Gamma", Disabled: true, Note: "no key"}},
		Match: func(it PickItem, q string) bool { return strings.Contains(strings.ToLower(it.Label), q) },
	}
}

func TestPickerTabsFilter(t *testing.T) {
	p := testPicker()
	if n := len(p.Visible()); n != 3 {
		t.Fatalf("All: %d", n)
	}
	p.Handle(Key{Kind: KeyTab})
	if v := p.Visible(); len(v) != 2 || v[0].ID != "a" {
		t.Fatalf("Configured: %v", v)
	}
	p.Handle(Key{Kind: KeyRight})
	if v := p.Visible(); len(v) != 1 || v[0].ID != "g" {
		t.Fatalf("Not configured: %v", v)
	}
	p.Handle(Key{Kind: KeyLeft})
	p.Handle(Key{Kind: KeyLeft})
	if p.Tab != 0 {
		t.Fatalf("tab %d", p.Tab)
	}
	p.Handle(Key{Kind: KeyLeft}) // wraps to Search
	if !p.Tabs[p.Tab].Search {
		t.Fatalf("tab %d", p.Tab)
	}
}

func TestPickerTypingSearchesAndEnterChooses(t *testing.T) {
	p := testPicker()
	for _, r := range "gam" {
		p.Handle(Key{Kind: KeyRune, Rune: r})
	}
	if p.Tab != 3 || len(p.Visible()) != 1 {
		t.Fatalf("tab %d, %v", p.Tab, p.Visible())
	}
	p.Handle(Key{Kind: KeyBackspace})
	p.Handle(Key{Kind: KeyBackspace})
	p.Handle(Key{Kind: KeyBackspace}) // empty search shows everything
	if len(p.Visible()) != 3 {
		t.Fatalf("%v", p.Visible())
	}
	p.Handle(Key{Kind: KeyDown})
	done, it := p.Handle(Key{Kind: KeyEnter})
	if !done || it == nil || it.ID != "b" {
		t.Fatalf("done %v, %v", done, it)
	}
	if done, it := testPicker().Handle(Key{Kind: KeyEsc}); !done || it != nil {
		t.Fatal("Esc closes with nothing")
	}
}

func TestPickerLines(t *testing.T) {
	p := testPicker()
	lines := p.Lines(80, PickStyle{})
	text := strings.Join(lines, "\n")
	for _, want := range []string{"[All]", "›  Alpha", "● Beta", "Gamma  · no key", "Esc close"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	for _, l := range p.Lines(30, PickStyle{}) {
		if Width(l) > 29 {
			t.Errorf("too wide: %q", l)
		}
	}
}
