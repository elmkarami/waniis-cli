// Package render turns session events into readable terminal lines: one event becomes one or a
// few lines, streamed agent text is joined, and charts and tables in answers are drawn.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/style"
)

// Options say how events are shown.
type Options struct {
	Style      style.Style
	Width      int  // terminal columns (0: 80)
	Charts     bool // draw plots above chart tables
	Live       bool // stream agent text as it arrives and show tool calls when they start
	Timestamps bool
	// People names the people in the session: the signed-in person reads "you", others by their
	// name, anyone unknown "someone"; "rafiiq" and agents read "Waniis". Ids are never shown.
	People *People
	// Names is a plain id-to-name map, used when People is nil.
	Names map[string]string
	// Chat is the interactive chat's transcript: a run's end is one quiet line ("✓ done · 6s")
	// and the start of a run is not announced (the live status line shows it).
	Chat bool
	// Own recognizes the echoes of messages this chat sent, which are not shown again.
	Own *Own
}

type toolCall struct {
	name, args string
}

// Renderer writes events to w. It is stateful (it pairs tool calls with results and joins
// streamed deltas), so use one per stream and call Flush at the end.
type Renderer struct {
	w io.Writer
	o Options

	agent       *MD
	agentRun    string
	streamed    map[string]bool
	deltas      map[string]*strings.Builder
	deltaOrder  []string
	tools       map[string]toolCall
	toolOrder   []string
	promptModes map[string]string

	answer   map[string]string // run id to the text of its streamed answer (deltas)
	messages map[string]string // run id to the text of its answer messages
	started  map[string]string // run id to when it started
	recent   []recentMsg       // the last messages shown, to show one line per message
}

type recentMsg struct {
	typ, author, text string
	seq               int
}

// New builds a renderer.
func New(w io.Writer, o Options) *Renderer {
	if o.Width <= 0 {
		o.Width = 80
	}
	if o.People == nil {
		o.People = NewPeople("", o.Names)
	}
	return &Renderer{w: w, o: o, streamed: map[string]bool{}, deltas: map[string]*strings.Builder{},
		tools: map[string]toolCall{}, promptModes: map[string]string{}, answer: map[string]string{}, messages: map[string]string{}, started: map[string]string{}}
}

func (r *Renderer) st() style.Style { return r.o.Style }

func (r *Renderer) name(id string) string { return r.o.People.Name(id) }

func (r *Renderer) mdOpts() MDOptions {
	return MDOptions{Style: r.o.Style, Width: r.o.Width - 8, Draw: r.o.Charts}
}

func (r *Renderer) closeAgent() {
	if r.agent != nil {
		r.agent.Flush()
		r.agent = nil
		r.agentRun = ""
	}
}

func (r *Renderer) timeLabel(ev api.Event) string {
	if !r.o.Timestamps {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ev.At)
	if err != nil {
		return ""
	}
	return r.st().Dim(t.UTC().Format("15:04:05")) + " "
}

// line writes one event line (continuation lines are indented).
func (r *Renderer) line(ev api.Event, text string) {
	r.closeAgent()
	ts := r.timeLabel(ev)
	for i, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if i == 0 {
			fmt.Fprintln(r.w, ts+l)
		} else {
			fmt.Fprintln(r.w, strings.Repeat(" ", visibleLen(ts))+"  "+l)
		}
	}
}

func visibleLen(s string) int { return style.Width(s) }

// agentText writes an agent answer as a block under "Waniis:".
func (r *Renderer) agentText(ev api.Event, text string) {
	r.closeAgent()
	ts := r.timeLabel(ev)
	iw := &indenter{w: r.w, first: ts + r.st().Bold(r.st().Cyan("Waniis:")) + " ", rest: strings.Repeat(" ", visibleLen(ts)) + "  "}
	m := NewMD(iw, r.mdOpts())
	m.Write(text)
	m.Flush()
}

// Event renders one event.
func (r *Renderer) Event(ev api.Event) {
	if ev.Private != nil && ev.Private.Hidden {
		switch ev.Type {
		case "tool.call", "tool.result", "mcp.tool_call", "agent.message", "agent.message.delta":
			if ev.Type == "agent.message.delta" || ev.Type == "tool.result" {
				return
			}
			r.line(ev, r.st().Dim(fmt.Sprintf("◌ private %s from %s (%s): only they can see it",
				ev.Private.Kind, orStr(ev.Private.OwnerName, r.name(ev.Private.OwnerID)), orStr(ev.Private.Title, ev.Private.Server))))
			return
		}
	}
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	s := str(p)
	st := r.st()
	switch ev.Type {
	case "agent.message.delta":
		r.delta(ev, s("runId"), s("text"))
		return
	case "agent.message":
		r.message(ev, s("runId"), s("text"))
		return
	case "tool.call":
		r.toolCall(ev, p)
		return
	case "tool.result":
		r.toolResult(ev, p)
		return
	case "prompt.queued", "chat.message":
		r.userMessage(ev, p)
		return
	case "prompt.started":
		r.started[s("runId")] = ev.At
		if r.o.Chat {
			return
		}
	case "run.steered":
		if r.o.Chat && s("authorId") != "" && s("authorId") == r.o.People.Self() {
			return // the chat already showed the /steer line
		}
	case "run.completed":
		r.flushTools()
		r.flushDeltas(s("runId"))
		r.runCompleted(ev, p)
		return
	}
	if text := r.describe(ev, p); text != "" {
		r.line(ev, text)
	}
	_ = st
}

// userMessage shows what a person said, once: a message can arrive as a chat message and as a
// prompt, and one you sent from this chat is already on screen.
func (r *Renderer) userMessage(ev api.Event, p map[string]any) {
	s := str(p)
	st := r.st()
	author, text := s("authorId"), s("text")
	if ev.Type == "prompt.queued" {
		tag := ""
		switch {
		case s("mode") == "plan":
			tag = "plan"
		case p["askOnly"] == true:
			tag = "ask"
		}
		r.promptModes[s("promptId")] = tag
		if s("continues") != "" {
			return // the conversation's own message, handed to a sandbox: already shown
		}
	}
	if r.o.Own != nil && r.o.Own.Match(ev.Type, author, r.o.People.Self(), s("promptId"), text) {
		return
	}
	for i, m := range r.recent {
		if m.typ != ev.Type && m.author == author && m.text == text && abs(m.seq-ev.Seq) <= 20 {
			r.recent = append(r.recent[:i], r.recent[i+1:]...)
			if pos := num(p, "position"); ev.Type == "prompt.queued" && pos > 0 {
				r.line(ev, st.Dim(fmt.Sprintf("◦ queued, position %d", pos)))
			}
			return
		}
	}
	r.recent = append(r.recent, recentMsg{ev.Type, author, text, ev.Seq})
	if len(r.recent) > 8 {
		r.recent = r.recent[1:]
	}
	txt := st.Bold("›") + " " + st.Bold(r.name(author)+":") + " " + text
	if ev.Type == "prompt.queued" {
		if tag := r.promptModes[s("promptId")]; tag != "" {
			txt += st.Dim("  [" + tag + "]")
		}
		if pos := num(p, "position"); pos > 0 {
			txt += st.Dim(fmt.Sprintf("  [queued, position %d]", pos))
		}
	} else if b, _ := p["offRecord"].(bool); b {
		txt += st.Dim("  [off the record]")
	}
	r.line(ev, txt)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (r *Renderer) delta(ev api.Event, run, text string) {
	r.answer[run] += text
	if !r.o.Live {
		b := r.deltas[run]
		if b == nil {
			b = &strings.Builder{}
			r.deltas[run] = b
			r.deltaOrder = append(r.deltaOrder, run)
		}
		b.WriteString(text)
		return
	}
	if r.agent == nil || r.agentRun != run {
		r.closeAgent()
		ts := r.timeLabel(ev)
		iw := &indenter{w: r.w, first: ts + r.st().Bold(r.st().Cyan("Waniis:")) + " ", rest: strings.Repeat(" ", visibleLen(ts)) + "  "}
		if r.streamed[run] { // the answer goes on after something else was printed: no second label
			iw.first = iw.rest
		}
		r.agent, r.agentRun = NewMD(iw, r.mdOpts()), run
	}
	r.streamed[run] = true
	r.agent.Write(text)
}

func (r *Renderer) message(ev api.Event, run, text string) {
	r.messages[run] += text
	if r.o.Live && r.streamed[run] {
		r.closeAgent() // the deltas already showed it
		return
	}
	delete(r.deltas, run)
	r.agentText(ev, text)
}

func (r *Renderer) toolCall(ev api.Event, p map[string]any) {
	id, _ := p["toolCallId"].(string)
	name, _ := p["tool"].(string)
	name = DisplayToolName(name)
	args, _ := p["args"].(map[string]any)
	tc := toolCall{name: name, args: argSummary(args)}
	if r.o.Live {
		r.line(ev, r.st().Dim("⚙ tool "+strings.TrimSpace(name+" "+tc.args)))
		r.tools[id] = tc
		return
	}
	r.tools[id] = tc
	r.toolOrder = append(r.toolOrder, id)
}

func (r *Renderer) toolResult(ev api.Event, p map[string]any) {
	id, _ := p["toolCallId"].(string)
	status, _ := p["status"].(string)
	summary, _ := p["summary"].(string)
	tc, known := r.tools[id]
	mark := r.st().Green("✓")
	if status == "error" {
		mark = r.st().Red("✗")
		if summary != "" {
			mark += " " + oneLine(summary, 100)
		}
	}
	if r.o.Live {
		if known {
			r.line(ev, r.st().Dim("  ")+mark)
		} else {
			r.line(ev, "⚙ tool result "+mark)
		}
		delete(r.tools, id)
		return
	}
	label := "⚙ tool result"
	if known {
		label = "⚙ tool " + strings.TrimSpace(tc.name+" "+tc.args)
		delete(r.tools, id)
		for i, o := range r.toolOrder {
			if o == id {
				r.toolOrder = append(r.toolOrder[:i], r.toolOrder[i+1:]...)
				break
			}
		}
	}
	r.line(ev, r.st().Dim(label)+" "+mark)
}

// flushTools prints the tool calls that never got a result.
func (r *Renderer) flushTools() {
	for _, id := range r.toolOrder {
		if tc, ok := r.tools[id]; ok {
			r.line(api.Event{}, r.st().Dim("⚙ tool "+strings.TrimSpace(tc.name+" "+tc.args)+" …"))
			delete(r.tools, id)
		}
	}
	r.toolOrder = nil
}

// Prime tells the renderer about events that were shown earlier (or not at all), so that what
// follows is understood: a run that began before the stream was joined still gets its duration.
func (r *Renderer) Prime(events []api.Event) {
	for _, ev := range events {
		if ev.Type == "prompt.started" {
			var p struct {
				RunID string `json:"runId"`
			}
			_ = json.Unmarshal(ev.Payload, &p)
			r.started[p.RunID] = ev.At
		}
	}
}

// flushDeltas writes the answer a run streamed when no final message came to replace it.
func (r *Renderer) flushDeltas(run string) {
	if b, ok := r.deltas[run]; ok {
		r.agentText(api.Event{}, b.String())
		delete(r.deltas, run)
		for i, d := range r.deltaOrder {
			if d == run {
				r.deltaOrder = append(r.deltaOrder[:i], r.deltaOrder[i+1:]...)
				break
			}
		}
	}
}

// Flush finishes the output: an open agent line ends, buffered text and unfinished tool calls are
// written.
func (r *Renderer) Flush() {
	r.closeAgent()
	r.flushTools()
	for _, run := range r.deltaOrder {
		if b, ok := r.deltas[run]; ok {
			r.agentText(api.Event{}, b.String())
			delete(r.deltas, run)
		}
	}
	r.deltaOrder = nil
}

// sameAsAnswer reports whether summary only repeats what the run already answered (the streamed
// text or its messages), ignoring spacing and a cut-off ending.
func (r *Renderer) sameAsAnswer(run, summary string) bool {
	norm := func(x string) string { return strings.TrimSuffix(strings.Join(strings.Fields(x), " "), "…") }
	sum := norm(summary)
	if sum == "" {
		return true
	}
	for _, a := range []string{r.answer[run], r.messages[run]} {
		a = norm(a)
		if a != "" && (a == sum || (len(sum) >= 20 && strings.HasPrefix(a, sum)) || (len(a) >= 20 && strings.HasPrefix(sum, a) && len(sum)-len(a) < 4)) {
			return true
		}
	}
	return false
}

// duration is how long a run took, from the timestamps of its start and end events.
func (r *Renderer) duration(run, end string) string {
	t0, err0 := time.Parse(time.RFC3339, r.started[run])
	t1, err1 := time.Parse(time.RFC3339, end)
	if err0 != nil || err1 != nil || t1.Before(t0) {
		return ""
	}
	return FormatElapsed(t1.Sub(t0))
}

// FormatElapsed writes a duration the way the chat shows it: "6s", "1m 05s".
func FormatElapsed(d time.Duration) string {
	sec := int(d.Round(time.Second) / time.Second)
	switch {
	case d < 500*time.Millisecond:
		return "<1s"
	case sec < 60:
		return fmt.Sprintf("%ds", sec)
	case sec < 3600:
		return fmt.Sprintf("%dm %02ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%dh %02dm", sec/3600, sec%3600/60)
}

// runCompleted ends a run: in the chat one quiet line, in logs the classic line. The run's
// summary is shown only when it says something the answer didn't.
func (r *Renderer) runCompleted(ev api.Event, p map[string]any) {
	st := r.st()
	s := str(p)
	run := s("runId")
	summary := strings.TrimSpace(s("summary"))
	dup := r.sameAsAnswer(run, summary)
	cost := ""
	if c, ok := p["costUsd"].(float64); ok && c > 0 {
		cost = fmt.Sprintf("$%.2f", c)
	}
	if !r.o.Chat {
		sum := ""
		if !dup {
			sum = ": " + oneLine(summary, 400)
		}
		if cost != "" {
			cost = st.Dim(" (" + cost + ")")
		}
		switch s("outcome") {
		case "success":
			r.line(ev, st.Green("■ run completed")+sum+cost)
		case "failed":
			if dup && summary != "" {
				sum = ": " + oneLine(summary, 400)
			}
			r.line(ev, st.Red("■ run failed")+sum+cost)
		case "stopped":
			r.line(ev, st.Yellow("■ run stopped")+sum+cost)
		case "needs_input":
			r.line(ev, st.Yellow("■ run needs input")+sum+cost)
		default:
			r.line(ev, "■ run "+s("outcome")+sum+cost)
		}
		return
	}
	var tail []string
	if d := r.duration(run, ev.At); d != "" {
		tail = append(tail, d)
	}
	if cost != "" {
		tail = append(tail, cost)
	}
	if !dup && s("outcome") != "failed" {
		r.line(ev, st.Bold("Summary:")+" "+oneLine(summary, 400))
	}
	more := func() string {
		if len(tail) == 0 {
			return ""
		}
		return st.Dim(" · " + strings.Join(tail, " · "))
	}
	switch s("outcome") {
	case "success":
		r.line(ev, st.Green("✓")+st.Dim(" done")+more())
	case "failed":
		reason := ""
		if summary != "" {
			reason = st.Dim(" · " + oneLine(summary, 200))
		}
		r.line(ev, st.Red("✗ failed")+reason+more())
	case "stopped":
		r.line(ev, st.Yellow("■ stopped")+more())
	case "needs_input":
		r.line(ev, st.Yellow("? needs your input")+more())
	default:
		r.line(ev, st.Dim("■ "+s("outcome"))+more())
	}
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func str(p map[string]any) func(string) string {
	return func(k string) string {
		v, _ := p[k].(string)
		return v
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// argSummary picks the argument a person would recognize a tool call by.
func argSummary(args map[string]any) string {
	for _, k := range []string{"path", "file", "filePath", "file_path", "command", "cmd", "query", "url", "pattern", "name", "id"} {
		if v, ok := args[k].(string); ok && v != "" {
			return oneLine(v, 80)
		}
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := args[k].(string); ok && v != "" {
			return oneLine(v, 80)
		}
	}
	return ""
}

// DisplayToolName shows the sandbox's own browser tools by the product's name (DEC-141):
// `rafiiq-browser_http_request` reads `waniis-browser_http_request`.
func DisplayToolName(name string) string {
	l := strings.ToLower(name)
	if strings.HasPrefix(l, "rafiiq-") || strings.HasPrefix(l, "rafiiq_") {
		return "waniis" + name[len("rafiiq"):]
	}
	return name
}
