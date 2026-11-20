package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/mark"
	"github.com/elmkarami/waniis-cli/internal/style"
	"github.com/elmkarami/waniis-cli/internal/upgrade"
)

// Logo size in pixels: 16×8 is four lines of half blocks, the smallest size where the W, the dot
// in its first V and the loop all read.
const logoW, logoH = 16, 8

// bannerFacts is what the welcome shows beside the mark.
type bannerFacts struct {
	Version   string
	Model     string // the model's name, or "" when unknown
	Workspace string // "Acme (team)", "personal", or ""
	Dir       string // the working directory, home shortened to ~
	Repo      string // owner/name of this checkout's origin, or ""
	Notices   []string
}

// bannerLines lays the welcome out: the mark on the left, four lines beside it, then the notices
// under it, each after a corner like a tree's last branch.
func bannerLines(st style.Style, f bannerFacts, color mark.Color) []string {
	logo := mark.Logo(logoW, logoH, st.On, color)
	where := f.Dir
	if f.Repo != "" {
		where += " · " + f.Repo
	}
	var who []string
	if f.Model != "" {
		who = append(who, f.Model)
	}
	if f.Workspace != "" {
		who = append(who, f.Workspace)
	}
	version := "v" + strings.TrimPrefix(f.Version, "v")
	if !upgrade.IsVersion(f.Version) {
		version = "development build"
	}
	side := []string{
		st.Bold("Waniis") + " " + st.Dim(version),
		st.Dim(strings.Join(who, " · ")),
		st.Dim(where),
		"",
	}
	var out []string
	for i := range side {
		l := ""
		if i < len(logo) {
			l = logo[i]
		}
		pad := logoW - style.Width(l)
		line := strings.TrimRight(" "+l+strings.Repeat(" ", max(0, pad))+"   "+side[i], " ")
		out = append(out, line)
	}
	for _, n := range f.Notices {
		out = append(out, "", "  "+st.Dim("└")+" "+n)
	}
	return out
}

// workspaceLabel names the workspace the CLI works in: the one chosen, or the only one.
func workspaceLabel(me *api.Me, chosen string) string {
	if me == nil {
		return ""
	}
	pick := func(name, kind string) string {
		if kind == "personal" {
			return "personal workspace"
		}
		return name
	}
	for _, w := range me.Workspaces {
		if chosen != "" && w.ID == chosen {
			return pick(w.Name, w.Kind)
		}
	}
	if chosen == "" && len(me.Workspaces) == 1 {
		w := me.Workspaces[0]
		return pick(w.Name, w.Kind)
	}
	return ""
}

// shortDir is dir with the home folder written ~.
func shortDir(dir string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && (dir == home || strings.HasPrefix(dir, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}

// welcome prints the banner (a terminal with colors) or one plain line.
func (s *chatSession) welcome(me *api.Me) {
	if !s.con.Raw || !s.sty.On {
		s.say(s.sty.Bold("Waniis") + " " + s.sty.Dim("chat. Type your first message to start a session; / for commands, Ctrl-C twice to leave."))
		return
	}
	f := bannerFacts{Version: Version, Workspace: workspaceLabel(me, s.rt.Client.Workspace)}
	if dir, err := os.Getwd(); err == nil {
		f.Dir = shortDir(dir)
	}
	if s.a.GitOrigin != nil {
		if n, ok := ParseRepo(s.a.GitOrigin()); ok {
			f.Repo = n
		}
	}
	// The model's name: the one chosen for this chat, else the workspace's; never wait long.
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	if m, err := s.rt.Client.Models(ctx); err == nil {
		ref := s.model
		if locked, _ := m.Locked(); locked {
			ref = ""
		}
		switch {
		case ref == "" && m.Routed():
			f.Model = "model chosen by task"
		default:
			if ref == "" {
				ref = m.Default()
			}
			f.Model = titleOf(m, ref)
			if f.Model == "its default" {
				f.Model = ""
			}
		}
	}
	if s.a.notifierOn("chat") {
		if n := s.a.updateNotice(); n != "" {
			f.Notices = append(f.Notices, s.sty.Yellow(n))
		}
	}
	if f.Repo == "" && !s.noRepo && s.id == "" {
		f.Notices = append(f.Notices, s.sty.Dim("No git repo here: your first message starts a conversation without a repo."))
	}
	for _, l := range bannerLines(s.sty, f, mark.DetectColor(s.a.Getenv)) {
		s.say(l)
	}
	s.say("")
}

// modes is the order Shift+Tab cycles the mode in.
var modes = []string{"auto", "build", "plan", "ask"}

// cycleMode moves to the next mode (Shift+Tab).
func (s *chatSession) cycleMode() {
	s.mu.Lock()
	cur := s.mode
	if cur == "" {
		cur = "auto"
	}
	next := modes[0]
	for i, m := range modes {
		if m == cur {
			next = modes[(i+1)%len(modes)]
		}
	}
	s.mode = next
	s.refreshPrompt()
	s.mu.Unlock()
}

// footerFor is the line under the input: the mode, how to change it, and the model if chosen.
func footerFor(st style.Style, mode, model string) string {
	if mode == "" {
		mode = "auto"
	}
	line := st.Yellow("►► "+mode+" mode") + st.Dim(" (shift+tab to cycle)")
	if model != "" {
		line += st.Dim(" · " + model)
	}
	return line
}
