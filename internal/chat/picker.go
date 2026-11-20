package chat

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// PickItem is one row of a Picker.
type PickItem struct {
	ID       string
	Label    string
	Detail   string // shown dim after the label
	Note     string // why it can't be chosen; shown dim at the end
	Disabled bool   // shown, but Enter explains instead of choosing
	Current  bool   // marked as the one in use
}

// PickTab is one tab: its name and which items it shows. A Search tab filters by what is typed.
type PickTab struct {
	Name   string
	Show   func(PickItem) bool // nil: every item
	Search bool
}

// PickRows is how many items a Picker shows at once.
const PickRows = 8

// Picker is a list with tabs that opens under the input line ("/model"): ←/→ or Tab change the
// tab, ↑/↓ move, typing searches, Enter chooses and Esc closes. Match says whether an item fits
// the search words.
type Picker struct {
	Title string
	Tabs  []PickTab
	Items []PickItem
	Match func(item PickItem, query string) bool
	// Done is called once, outside the console's lock, with the chosen item or nil when closed.
	Done func(*PickItem)

	Tab   int
	Sel   int
	Query []rune
}

// Visible is what the current tab shows.
func (p *Picker) Visible() []PickItem {
	if len(p.Tabs) == 0 {
		return p.Items
	}
	tab := p.Tabs[p.Tab%len(p.Tabs)]
	q := strings.TrimSpace(string(p.Query))
	var out []PickItem
	for _, it := range p.Items {
		if tab.Show != nil && !tab.Show(it) {
			continue
		}
		if tab.Search && q != "" && p.Match != nil && !p.Match(it, q) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func (p *Picker) searchTab() int {
	for i, t := range p.Tabs {
		if t.Search {
			return i
		}
	}
	return -1
}

func (p *Picker) moveTab(d int) {
	if len(p.Tabs) > 0 {
		p.Tab = (p.Tab + d + len(p.Tabs)) % len(p.Tabs)
		p.Sel = 0
	}
}

// Handle applies one key. It reports whether the picker is finished, and the chosen item (nil
// when it was closed). Choosing a disabled item finishes it too, so the caller can say why.
func (p *Picker) Handle(k Key) (bool, *PickItem) {
	items := p.Visible()
	switch k.Kind {
	case KeyEsc, KeyCtrlC:
		return true, nil
	case KeyTab, KeyRight:
		p.moveTab(1)
	case KeyLeft:
		p.moveTab(-1)
	case KeyUp:
		if len(items) > 0 {
			p.Sel = (p.Sel - 1 + len(items)) % len(items)
		}
	case KeyDown:
		if len(items) > 0 {
			p.Sel = (p.Sel + 1) % len(items)
		}
	case KeyEnter:
		if len(items) == 0 {
			return false, nil
		}
		it := items[p.Sel%len(items)]
		return true, &it
	case KeyBackspace:
		if len(p.Query) > 0 {
			p.Query = p.Query[:len(p.Query)-1]
			p.Sel = 0
		}
	case KeyCtrlU:
		p.Query, p.Sel = nil, 0
	case KeyRune:
		// Typing searches, from any tab.
		if s := p.searchTab(); s >= 0 {
			p.Tab = s
		}
		p.Query = append(p.Query, k.Rune)
		p.Sel = 0
	}
	return false, nil
}

// PickStyle styles a Picker's lines; nil functions leave text plain.
type PickStyle struct {
	Bold, Dim, Accent, Reverse func(string) string
}

func apply(f func(string) string, s string) string {
	if f == nil {
		return s
	}
	return f(s)
}

// Lines draws the picker: a tab bar, the search field on the search tab, the rows and a hint.
func (p *Picker) Lines(w int, st PickStyle) []string {
	var out []string
	var bar strings.Builder
	if p.Title != "" {
		bar.WriteString(apply(st.Bold, p.Title) + "  ")
	}
	for i, t := range p.Tabs {
		if i > 0 {
			bar.WriteString(" ")
		}
		name := " " + t.Name + " "
		if i == p.Tab%len(p.Tabs) {
			if st.Reverse != nil {
				name = st.Reverse(name)
			} else {
				name = "[" + t.Name + "]"
			}
		} else {
			name = apply(st.Dim, name)
		}
		bar.WriteString(name)
	}
	out = append(out, cutVisible(bar.String(), w-1))
	if len(p.Tabs) > 0 && p.Tabs[p.Tab%len(p.Tabs)].Search {
		out = append(out, "  "+apply(st.Dim, "Search:")+" "+string(p.Query)+"▏")
	}
	items := p.Visible()
	if len(items) == 0 {
		out = append(out, apply(st.Dim, "  Nothing here."))
	}
	sel := 0
	if len(items) > 0 {
		sel = p.Sel % len(items)
	}
	from := 0
	if len(items) > PickRows {
		from = min(max(0, sel-PickRows/2), len(items)-PickRows)
	}
	labelW := 0
	for _, it := range items[from:min(len(items), from+PickRows)] {
		labelW = max(labelW, utf8.RuneCountInString(it.Label))
	}
	for i, it := range items[from:min(len(items), from+PickRows)] {
		mark := "  "
		if it.Current {
			mark = "● "
		}
		lead := " "
		if from+i == sel {
			lead = "›"
		}
		label := it.Label + strings.Repeat(" ", labelW-utf8.RuneCountInString(it.Label))
		rest := ""
		if it.Detail != "" {
			rest += "  " + it.Detail
		}
		if it.Note != "" {
			rest += "  · " + it.Note
		}
		line := truncate(lead+mark+label+rest, w-1)
		switch {
		case from+i == sel:
			line = apply(st.Bold, line)
		case it.Disabled:
			line = apply(st.Dim, line)
		case it.Current:
			line = apply(st.Accent, line)
		}
		out = append(out, line)
	}
	hint := "←/→ tabs · ↑/↓ move · type to search · Enter choose · Esc close"
	if len(items) > PickRows {
		hint = strconv.Itoa(sel+1) + "/" + strconv.Itoa(len(items)) + " · " + hint
	}
	out = append(out, apply(st.Dim, truncate("  "+hint, w-1)))
	return out
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n && n > 1 {
		return string(r[:n-1]) + "…"
	}
	return s
}
