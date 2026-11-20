package render

import (
	"fmt"
	"strings"

	"github.com/elmkarami/waniis-cli/internal/api"
)

func num(p map[string]any, k string) int {
	f, _ := p[k].(float64)
	return int(f)
}

func list(p map[string]any, k string) []any {
	l, _ := p[k].([]any)
	return l
}

func obj(p map[string]any, k string) map[string]any {
	m, _ := p[k].(map[string]any)
	return m
}

func strs(l []any) []string {
	var out []string
	for _, v := range l {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func ms(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%dms", n)
	}
	return fmt.Sprintf("%.1fs", float64(n)/1000)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// describe is the line (or few lines) for an event, "" for events people don't need to see.
func (r *Renderer) describe(ev api.Event, p map[string]any) string {
	st := r.st()
	s := str(p)
	switch ev.Type {
	case "session.created":
		return st.Dim("● session started: " + oneLine(s("title"), 100))
	case "session.repos_set":
		txt := "● repo: " + strings.Join(strs(list(p, "repos")), ", ")
		if in := obj(p, "inferred"); in != nil {
			txt += " (guessed: " + oneLine(fmt.Sprint(in["reason"]), 100) + ")"
		}
		return st.Dim(txt)
	case "session.working":
		return st.Dim("● working in a sandbox on " + strings.Join(strs(list(p, "repos")), ", "))
	case "conversation.work_proposed":
		return st.Yellow("? Work on this in a sandbox? ") + oneLine(s("task"), 160) + st.Dim(" (accept with /work in `waniis chat`, or in the web app)")
	case "session.repo_added":
		return st.Dim("● repo added: " + s("repo"))
	case "session.repo_checked_out":
		return st.Dim("● checked out " + s("repo") + " at " + s("dir"))
	case "participant.joined":
		return st.Dim("● " + r.name(s("userId")) + " joined")
	case "participant.left":
		return st.Dim("● " + r.name(s("userId")) + " left")
	case "session.owner_changed":
		return st.Dim("● owner changed to " + r.name(s("to")))
	case "session.archived":
		return st.Dim("● session archived")
	case "session.restored":
		return st.Dim("● session restored")
	case "prompt.started":
		if tag := r.promptModes[s("promptId")]; tag != "" {
			return st.Cyan("▶ run started") + st.Dim(" ("+tag+")")
		}
		return st.Cyan("▶ run started")
	case "prompt.cancelled":
		return st.Dim("◦ prompt cancelled")
	case "prompt.edited":
		return st.Dim("◦ prompt edited by " + r.name(s("editedBy")))
	case "look.started":
		return st.Dim("◦ " + s("command") + " started")
	case "look.completed":
		return st.Dim("◦ lookup " + s("status"))
	case "sandbox.status":
		return r.sandbox(p)
	case "agent.retrying":
		return st.Yellow(fmt.Sprintf("↻ model error, retry %d: %s", num(p, "attempt"), oneLine(s("message"), 120)))
	case "model.fallback":
		return st.Yellow(fmt.Sprintf("↻ model fallback %v to %v (%v)", p["from"], p["to"], p["reason"]))
	case "file.changed":
		return st.Dim(fmt.Sprintf("± %s %s (+%d −%d)", s("path"), s("changeType"), num(p, "additions"), num(p, "deletions")))
	case "run.stop_requested":
		return st.Dim("■ stop requested by " + r.name(s("requestedBy")))
	case "run.steered":
		return st.Bold("↪ "+r.name(s("authorId"))+" steered:") + " " + s("text")
	case "run.plain_summary":
		return st.Bold("Summary: ") + s("text")
	case "session.continued_from":
		return st.Dim("● continues an earlier session: " + oneLine(s("title"), 80))
	case "session.continued_in":
		return st.Dim("● continued in a new session")
	case "changes.kept":
		return st.Dim(fmt.Sprintf("◦ changes kept, not published: %s, +%d −%d", plural(num(p, "files"), "file", "files"), num(p, "additions"), num(p, "deletions")))
	case "changes.publish_requested":
		return st.Dim("◦ publish requested by " + r.name(s("requestedBy")))
	case "changes.discard_requested":
		return st.Dim("◦ discard requested by " + r.name(s("requestedBy")))
	case "changes.discarded":
		return st.Dim("◦ changes discarded")
	case "pr.opened":
		txt := st.Green(fmt.Sprintf("PR #%d opened", num(p, "number")))
		if repo := s("repo"); repo != "" {
			txt = st.Green(fmt.Sprintf("PR %s#%d opened", repo, num(p, "number")))
		}
		txt += " " + s("url")
		if d, _ := p["draft"].(bool); d {
			txt += st.Dim(" (draft)")
		}
		return txt
	case "pr.updated":
		return r.prUpdated(p)
	case "pr.follow_up":
		return st.Dim(fmt.Sprintf("↻ PR #%d follow-up (%s, %s): %s", num(p, "number"), s("trigger"), s("action"), oneLine(s("summary"), 140)))
	case "verification.started":
		return st.Dim(fmt.Sprintf("… checking the change (attempt %d)", num(p, "attempt")))
	case "verification.completed":
		return r.verification(p)
	case "review.started":
		return st.Dim(fmt.Sprintf("… self-review, round %d", num(p, "round")))
	case "review.completed":
		n := len(list(p, "findings"))
		txt := fmt.Sprintf("review: %s, %s", s("status"), plural(n, "finding", "findings"))
		if sm := s("summary"); sm != "" {
			txt += " — " + oneLine(sm, 140)
		}
		return st.Dim("◦ " + txt)
	case "plan.proposed":
		return st.Yellow("? plan proposed") + st.Dim(fmt.Sprintf(" (revision %d): ", num(p, "revision"))) + oneLine(s("task"), 140)
	case "plan.approved":
		return st.Dim("◦ plan approved by " + r.name(s("approvedBy")))
	case "plan.changes_requested":
		return st.Dim("◦ plan changes requested by " + r.name(s("requestedBy")) + ": " + oneLine(s("feedback"), 140))
	case "plan.discarded":
		return st.Dim("◦ plan discarded")
	case "child.spawned":
		return st.Dim("◦ child session started: " + oneLine(s("title"), 100))
	case "child.completed":
		return st.Dim("◦ child session " + s("status") + ": " + oneLine(s("summary"), 140))
	case "error":
		return st.Red("✗ error "+s("code")+": ") + oneLine(s("message"), 200)
	case "mcp.tool_call":
		return r.mcpCall(p)
	case "mcp.approval_requested":
		return st.Yellow("⏸ needs approval: ") + s("server") + "." + s("tool") + st.Dim(" ("+s("risk")+" tool)") + argLine(obj(p, "args"))
	case "mcp.approval_decided":
		return st.Dim(fmt.Sprintf("▶ approval for %s.%s: %s", s("server"), s("tool"), s("decision")))
	case "mcp.connection_needed":
		return st.Yellow("⏸ needs a connection: ") + s("title") + st.Dim(" ("+s("reason")+")")
	case "sandbox.egress_blocked":
		return st.Red("⛔ blocked ") + s("host") + " (" + s("method") + ")" + st.Dim(" — "+oneLine(s("message"), 120))
	case "network.access_requested":
		return st.Yellow("⏸ needs approval: ") + "network access to " + s("host") + " (" + s("access") + ")" +
			st.Dim(" — "+oneLine(s("reason"), 120))
	case "network.access_decided":
		return st.Dim(fmt.Sprintf("▶ network access to %s: %s", s("host"), s("decision")))
	case "network.host_allowed":
		return st.Dim(fmt.Sprintf("▶ %s allowed (%s, %s) by %s", s("host"), s("access"), s("scope"), r.name(s("by"))))
	case "network.admin_asked":
		return st.Dim("◦ " + r.name(s("by")) + " asked an admin to allow " + s("host"))
	case "environment.proposed":
		return st.Yellow("? environment setup proposed") + st.Dim(" for "+s("repo")+"; decide in the web app")
	case "environment.proposal_decided":
		return st.Dim("◦ environment proposal " + s("decision"))
	case "guardrails.escalated":
		return st.Yellow("⏸ guardrails escalated the request")
	case "engineer_review.requested":
		return st.Dim("◦ engineer review requested by " + r.name(s("requestedBy")))
	}
	return ""
}

func argLine(a map[string]any) string {
	if sm := argSummary(a); sm != "" {
		return "\n" + sm
	}
	return ""
}

func (r *Renderer) sandbox(p map[string]any) string {
	st := r.st()
	s := str(p)
	if step := obj(p, "step"); step != nil {
		mark := st.Green("✓")
		if ok, _ := step["ok"].(bool); !ok {
			mark = st.Red("✗")
		}
		return st.Dim("◦ setup ") + mark + st.Dim(fmt.Sprintf(" %v (%s)", step["title"], ms(num(step, "ms"))))
	}
	txt := "◦ sandbox " + s("status")
	if c := s("command"); c != "" {
		txt += ": " + oneLine(c, 100)
	} else if d := s("detail"); d != "" {
		txt += ": " + oneLine(d, 100)
	}
	if s("status") == "error" {
		return st.Red(txt)
	}
	return st.Dim(txt)
}

func (r *Renderer) prUpdated(p map[string]any) string {
	st := r.st()
	s := str(p)
	n := num(p, "number")
	by := ""
	if b := s("by"); b != "" {
		by = " by " + b
	}
	switch s("state") {
	case "merged":
		return st.Green(fmt.Sprintf("PR #%d merged", n)) + by
	case "closed":
		return st.Yellow(fmt.Sprintf("PR #%d closed", n)) + by
	case "pushed":
		return st.Dim(fmt.Sprintf("PR #%d: %s pushed%s", n, plural(num(p, "commits"), "commit", "commits"), by))
	case "reviewed":
		return st.Dim(fmt.Sprintf("PR #%d review: %v%s", n, p["review"], by))
	case "checks":
		if f := strs(list(p, "failed")); len(f) > 0 {
			return st.Red(fmt.Sprintf("PR #%d checks: %d passed, failed: %s", n, num(p, "passed"), strings.Join(f, ", ")))
		}
		return st.Green(fmt.Sprintf("PR #%d checks: %d passed", n, num(p, "passed")))
	}
	return st.Dim(fmt.Sprintf("PR #%d %s%s", n, strings.ReplaceAll(s("state"), "_", " "), by))
}

func (r *Renderer) verification(p map[string]any) string {
	st := r.st()
	var parts []string
	for _, rp := range list(p, "proofs") {
		bundle := obj(rp.(map[string]any), "bundle")
		for _, c := range list(bundle, "checks") {
			cm, _ := c.(map[string]any)
			kind := fmt.Sprint(cm["kind"])
			switch cm["status"] {
			case "pass":
				if counts := obj(cm, "counts"); counts != nil && kind == "tests" {
					parts = append(parts, fmt.Sprintf("tests %d passed", num(counts, "passed")))
				} else {
					parts = append(parts, kind)
				}
			case "fail":
				parts = append(parts, st.Red(kind+" failed"))
			default:
				parts = append(parts, st.Dim(fmt.Sprintf("%s %v", kind, cm["status"])))
			}
		}
	}
	overall := fmt.Sprint(p["overall"])
	head := st.Green("✓ checks")
	switch overall {
	case "partially_verified":
		head = st.Yellow("! checks partly verified")
	case "unverified":
		head = st.Red("✗ checks unverified")
	}
	txt := head
	if len(parts) > 0 {
		txt += ": " + strings.Join(parts, ", ")
	}
	if f, _ := p["fixing"].(bool); f {
		txt += st.Dim(" — the agent is fixing the failures")
	}
	return txt
}

func (r *Renderer) mcpCall(p map[string]any) string {
	st := r.st()
	s := str(p)
	mark := st.Green("✓")
	switch s("status") {
	case "error":
		mark = st.Red("✗ " + oneLine(s("error"), 80))
	case "denied":
		mark = st.Red("denied")
	}
	args := argSummary(obj(p, "args"))
	return st.Dim("⚙ tool "+strings.TrimSpace(DisplayToolName(s("server"))+"."+s("tool")+" "+args)) + " " + mark + st.Dim(" ("+ms(num(p, "durationMs"))+")")
}
