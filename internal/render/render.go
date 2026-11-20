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
	// Names maps user ids to what people read ("maya"); "rafiiq" and agents read "Waniis".
	Names map[string]string
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
}

// New builds a renderer.
func New(w io.Writer, o Options) *Renderer {
	if o.Width <= 0 {
		o.Width = 80
	}
	return &Renderer{w: w, o: o, streamed: map[string]bool{}, deltas: map[string]*strings.Builder{},
		tools: map[string]toolCall{}, promptModes: map[string]string{}}
}

func (r *Renderer) st() style.Style { return r.o.Style }

func (r *Renderer) name(id string) string {
	switch id {
	case "":
		return "someone"
	case "rafiiq", "waniis":
		return "Waniis"
	}
	if n := r.o.Names[id]; n != "" {
		return n
	}
	return id
}

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
	case "run.completed":
		r.flushTools()
	}
	if text := r.describe(ev, p); text != "" {
		r.line(ev, text)
	}
	_ = st
}

func (r *Renderer) delta(ev api.Event, run, text string) {
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
		r.agent, r.agentRun = NewMD(iw, r.mdOpts()), run
	}
	r.streamed[run] = true
	r.agent.Write(text)
}

func (r *Renderer) message(ev api.Event, run, text string) {
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
