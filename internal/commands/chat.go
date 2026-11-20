package commands

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/chat"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/export"
	"github.com/elmkarami/waniis-cli/internal/mark"
	"github.com/elmkarami/waniis-cli/internal/output"
	"github.com/elmkarami/waniis-cli/internal/render"
	"github.com/elmkarami/waniis-cli/internal/sessionstate"
	"github.com/elmkarami/waniis-cli/internal/stream"
	"github.com/elmkarami/waniis-cli/internal/style"
)

type chatOptions struct {
	NoRepo      bool
	Mode, Model string
}

func (a *app) chatCmd() *cobra.Command {
	var o chatOptions
	c := &cobra.Command{
		Use:   "chat [id]",
		Short: "Talk to Waniis in your terminal (line based, not full screen)",
		Long: `An interactive chat. Without an id the first line you type starts a new session on this
checkout's repo (--chat: a conversation with no repo). With an id it follows that session. Events
stream above the prompt line as they happen, and you can keep typing while Waniis works, with a
status line (what it is doing, for how long) above the prompt.

Enter sends a message (it is queued if a run is going). /steer <text> redirects the run that is
going. Type / to see the slash commands, Tab completes one, Up and Down pick, Esc closes the menu;
/help lists them all. Ctrl-C once stops following (the session keeps running); twice, or /quit,
leaves. Without a terminal on stdin the lines are read one by one, with no menu: pipe a prompt in
and the chat streams the run, then exits.`,
		Example: `  waniis                      # new chat in this repo
  waniis chat ses_01abc       # follow and talk to a session
  echo "why is CI red?" | waniis chat --chat`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			if err := oneOf("mode", o.Mode, validModes); err != nil {
				return err
			}
			return a.runChat(cmd.Context(), id, o)
		},
	}
	fl := c.Flags()
	fl.BoolVar(&o.NoRepo, "chat", false, "a conversation with no repo and no sandbox")
	fl.StringVar(&o.Mode, "mode", "", "how prompts run: auto|build|plan|ask")
	fl.StringVar(&o.Model, "model", "", "model for the prompts")
	return c
}

// question is something waiting for a y/N from the person.
type question struct {
	kind      string // net (agent asked), allow (a blocked host), tool, pick
	requestID string
	host      string
	text      string
	expires   time.Time
	picks     []string
}

type chatSession struct {
	a    *app // a copy of the app whose Out and Err are the console
	rt   *conn
	con  *chat.Console
	ed   *chat.Editor
	sty  style.Style
	ctx  context.Context
	opts chatOptions

	mu           sync.Mutex
	id, title    string
	st           *sessionstate.State
	rend         *render.Renderer
	following    bool
	cancelFollow context.CancelFunc
	gen          int
	queue        []question
	current      *question
	asked        map[string]bool
	cannotAllow  bool
	tracked      []string // prompts sent whose end we wait for at EOF
	runsWanted   int
	mode, model  string
	noRepo       bool
	ctrlC        int
	promptStr    atomic.Pointer[string]
	footerStr    atomic.Pointer[string]

	people  *render.People // who is who: you, the others, never an id
	own     *render.Own    // what this chat sent, so its echoes aren't shown twice
	act     *chat.Activity // the live status: what Waniis is doing, for how long
	tried   map[string]time.Time
	spinner bool          // draw the spinner (a terminal with colors)
	mark    *mark.Spinner // the turning Waniis mark, when the terminal has colors
	frame   atomic.Int32  // the spinner's frame

	work       chan string   // submitted lines, handled one at a time off the input loop
	workDone   chan struct{} // closed when the worker has handled everything submitted
	quit       chan struct{} // closed when a command asked to leave
	quitOnce   sync.Once
	tickerStop chan struct{}
}

func (a *app) runChat(ctx context.Context, id string, o chatOptions) error {
	rt, err := a.newClient(true)
	if err != nil {
		return err
	}
	ca := *a
	ca.g.JSON = false
	sty := style.Style{On: a.colorOn()}
	me, _ := rt.Client.Me(ctx)
	selfID := ""
	if me != nil {
		selfID = me.ID
	}
	s := &chatSession{rt: rt, ctx: ctx, opts: o, sty: sty, ed: chat.NewEditor(), asked: map[string]bool{},
		mode: o.Mode, model: o.Model, noRepo: o.NoRepo, st: sessionstate.New(),
		people: render.NewPeople(selfID, nil), own: render.NewOwn(a.Now), act: chat.NewActivity(a.Now),
		tried: map[string]time.Time{}, tickerStop: make(chan struct{})}
	s.refreshPrompt()
	raw := false
	var restore func()
	if a.StdinTTY && a.StdoutTTY && a.MakeRaw != nil {
		if r, err := a.MakeRaw(); err == nil {
			raw, restore = true, r
			defer restore()
		}
	}
	s.con = &chat.Console{Out: a.Out, Raw: raw, Width: a.termWidth, Ed: s.ed, Prompt: s.promptText, Footer: s.footerText, Bold: sty.Bold, Dim: sty.Dim,
		PickStyle: chat.PickStyle{Bold: sty.Bold, Dim: sty.Dim, Accent: sty.Cyan, Reverse: sty.Reverse}}
	ca.Out, ca.Err = s.con, s.con
	s.a = &ca
	s.spinner = raw && sty.On
	if s.spinner {
		s.mark = mark.NewSpinner(mark.DetectColor(a.Getenv))
	}
	if raw {
		go s.ticker()
	}

	s.welcome(me)
	if id != "" {
		if err := s.attach(id); err != nil {
			s.con.Hide()
			return err
		}
	}
	if raw {
		s.con.Redraw()
	}
	err = s.loop(raw)
	close(s.tickerStop)
	s.stopFollowing()
	s.con.Hide()
	s.farewell()
	if err != nil && !errors.Is(err, errQuit) {
		return err
	}
	return nil
}

var errQuit = errors.New("quit")

// promptText is read by the console while it holds its own lock, so it must not take s.mu.
func (s *chatSession) promptText() string {
	if p := s.promptStr.Load(); p != nil {
		return *p
	}
	return "› "
}

// footerText is read by the console while it holds its own lock, so it must not take s.mu.
func (s *chatSession) footerText() string {
	if f := s.footerStr.Load(); f != nil {
		return *f
	}
	return ""
}

// refreshPrompt recomputes the prompt (an open question shows "?") and the line under the input
// (the mode and the chosen model). Call with s.mu held.
func (s *chatSession) refreshPrompt() {
	p := "› "
	if s.current != nil {
		p = "? "
	}
	s.promptStr.Store(&p)
	f := footerFor(s.sty, s.mode, s.model)
	s.footerStr.Store(&f)
}

// say prints one line (or more) of the chat's own, closing any half-written answer first.
func (s *chatSession) say(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sayLocked(text)
}

func (s *chatSession) sayLocked(text string) {
	if s.rend != nil {
		s.rend.Flush()
	}
	fmt.Fprintln(s.con, text)
}

func (s *chatSession) dim(text string) { s.say(s.sty.Dim(text)) }

func (s *chatSession) farewell() {
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	if id == "" {
		fmt.Fprintln(s.con, "Bye.")
		return
	}
	fmt.Fprintf(s.con, "The session keeps running in Waniis; reopen with `waniis chat %s`.\n", id)
	fmt.Fprintln(s.con, sessionURL(s.rt.Base, id))
}

// ---- input ----

func (s *chatSession) loop(raw bool) error {
	s.work = make(chan string, 256)
	s.workDone = make(chan struct{})
	s.quit = make(chan struct{})
	// Lines are handled one at a time by a worker, so that reading the keyboard never waits for
	// the network and the person can keep typing while Waniis works.
	go func() {
		defer close(s.workDone)
		for line := range s.work {
			if s.handle(line) {
				s.quitOnce.Do(func() { close(s.quit) })
				return
			}
		}
	}()
	if raw {
		return s.rawLoop()
	}
	return s.plainLoop()
}

// submit shows what the person typed, as their message, and hands it to the worker.
func (s *chatSession) submit(line string) {
	if strings.TrimSpace(line) == "" && !s.hasQuestion() {
		s.con.Redraw()
		return
	}
	if t := strings.TrimSpace(line); t != "" {
		s.mu.Lock()
		s.sayLocked(s.sty.Dim("›") + " " + t)
		s.mu.Unlock()
	}
	select {
	case s.work <- line:
	case <-s.ctx.Done():
	}
}

func (s *chatSession) hasQuestion() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current != nil
}

// ticker redraws the status line: about ten times a second with a spinner, once a second
// (for the elapsed time) without.
func (s *chatSession) ticker() {
	every := time.Second
	if s.spinner {
		every = 100 * time.Millisecond
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.frame.Add(1)
			s.refreshStatus()
		case <-s.tickerStop:
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// refreshStatus draws the status line (a terminal only; nothing is drawn when output is piped).
func (s *chatSession) refreshStatus() {
	if s.con == nil || !s.con.Raw {
		return
	}
	if s.mark != nil {
		s.con.SetStatus(s.act.Block(s.sty, int(s.frame.Load()), s.mark))
		return
	}
	s.con.SetStatus(s.act.Line(s.sty, int(s.frame.Load()), s.spinner))
}

func (s *chatSession) plainLoop() error {
	r := bufio.NewReader(s.a.In)
	lines := make(chan string)
	go func() {
		defer close(lines)
		for {
			line, err := r.ReadString('\n')
			if line = strings.TrimRight(line, "\r\n"); line != "" || err == nil {
				select {
				case lines <- line:
				case <-s.ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-s.quit:
			return nil
		case line, ok := <-lines:
			if !ok { // end of input: handle what was sent, let the work finish, then leave
				close(s.work)
				select {
				case <-s.workDone:
				case <-s.ctx.Done():
					return nil
				}
				s.waitSettled()
				return nil
			}
			s.submit(line)
		}
	}
}

func (s *chatSession) rawLoop() error {
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 256)
		for {
			n, err := s.a.In.Read(buf)
			if n > 0 {
				select {
				case chunks <- append([]byte(nil), buf[:n]...):
				case <-s.ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-s.quit:
			return nil
		case chunk, ok := <-chunks:
			if !ok {
				return nil
			}
			for _, k := range chat.Decode(chunk) {
				if k.Kind != chat.KeyCtrlC {
					s.resetCtrlC()
				}
				// The footer is redrawn from the stream's goroutine too: change the input line under
				// the console's lock.
				var res chat.Result
				var line string
				var picked func()
				cycle := false
				s.con.Edit(func() {
					if k.Kind == chat.KeyBackTab && s.con.Pick == nil {
						cycle = true
						return
					}
					if p := s.con.Pick; p != nil {
						// An open picker (/model) takes the keys until it is done.
						if done, item := p.Handle(k); done {
							s.con.Pick = nil
							if p.Done != nil {
								picked = func() { p.Done(item) }
							}
						}
						return
					}
					res = s.ed.Handle(k)
					line = s.ed.Line
				})
				if picked != nil {
					s.con.Redraw()
					picked()
				}
				if cycle {
					s.cycleMode()
				}
				switch res {
				case chat.Submit:
					s.submit(line)
				case chat.Interrupt:
					if s.interrupt() {
						return nil
					}
				case chat.EOF:
					return nil
				case chat.ClearScr:
					s.con.Clear()
				}
				s.con.Redraw()
			}
		}
	}
}

func (s *chatSession) resetCtrlC() {
	s.mu.Lock()
	s.ctrlC = 0
	s.mu.Unlock()
}

// interrupt is Ctrl-C on an empty line: the first stops following, the second leaves.
func (s *chatSession) interrupt() bool {
	s.mu.Lock()
	s.ctrlC++
	n := s.ctrlC
	s.mu.Unlock()
	if n >= 2 {
		return true
	}
	if s.stopFollowing() {
		s.dim("Stopped following. The session keeps running in Waniis. Ctrl-C again to leave, or send a prompt to follow it again.")
	} else {
		s.dim("Ctrl-C again to leave.")
	}
	return false
}

// handle processes one submitted line; it reports whether to leave.
func (s *chatSession) handle(line string) bool {
	line = strings.TrimSpace(line)
	s.mu.Lock()
	cur := s.current
	s.mu.Unlock()
	if cur != nil {
		if s.answer(cur, line) {
			return false
		}
	}
	switch {
	case line == "":
		return false
	case strings.HasPrefix(line, "/"):
		return s.slash(line)
	}
	s.send(line)
	return false
}

// ---- following ----

func (s *chatSession) stopFollowing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	was := s.following
	if s.cancelFollow != nil {
		s.cancelFollow()
	}
	s.following = false
	s.act.Stop()
	s.refreshStatus()
	return was
}

func (s *chatSession) startFollowing() {
	s.mu.Lock()
	if s.following || s.id == "" {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancelFollow, s.following = cancel, true
	s.gen++
	gen, id, since := s.gen, s.id, s.st.LastSeq
	s.mu.Unlock()
	go func() {
		err := stream.Follow(ctx, s.rt.Client, id, stream.Options{Since: since, Sleep: s.a.Sleep, PollInterval: s.a.PollInterval,
			Notice: func(m string) { s.dim(m) }}, s.onEvent)
		s.mu.Lock()
		if s.gen == gen {
			s.following = false
			s.act.Stop()
		}
		s.mu.Unlock()
		s.refreshStatus()
		if err != nil && ctx.Err() == nil {
			s.dim("Lost the live connection (" + exitcode.As(err).Message + "). Send a prompt to follow again.")
		}
	}()
}

func (s *chatSession) onEvent(ev api.Event) error {
	s.ensurePeople(ev)
	s.mu.Lock()
	s.st.Apply(ev)
	if s.rend != nil {
		s.rend.Event(ev)
	}
	s.watch(ev)
	s.trackActivity(ev)
	s.mu.Unlock()
	s.refreshStatus()
	return nil
}

// trackActivity keeps the status line going while a run is, and says what it is doing. Called
// with s.mu held.
func (s *chatSession) trackActivity(ev api.Event) {
	switch ev.Type {
	case "prompt.started":
		s.act.Start()
	case "run.completed", "prompt.cancelled":
		if s.st.Idle() {
			s.act.Stop()
		}
	}
	s.act.Observe(ev)
}

// ensurePeople refreshes the session's people when an event mentions someone not known yet, so
// the event reads "Sara" and not "someone". A person already tried is looked for again only
// after a while.
func (s *chatSession) ensurePeople(ev api.Event) {
	unknown := s.people.Unknown(ev)
	if len(unknown) == 0 {
		return
	}
	s.mu.Lock()
	id, now, fresh := s.id, s.a.Now(), false
	for _, u := range unknown {
		if t, ok := s.tried[u]; !ok || now.Sub(t) > 30*time.Second {
			fresh, s.tried[u] = true, now
		}
	}
	s.mu.Unlock()
	if !fresh || id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	_ = s.a.fillPeople(ctx, s.rt, id, s.people)
}

func (s *chatSession) newRenderer() *render.Renderer {
	o := s.a.renderOpts(true, s.people)
	o.Chat, o.Own = true, s.own
	return render.New(s.con, o)
}

// attach starts following a session: its last events, then live.
func (s *chatSession) attach(id string) error {
	ctx := s.ctx
	sess, err := s.rt.Client.GetSession(ctx, id)
	if err != nil {
		return err
	}
	hist, err := stream.History(ctx, s.rt.Client, id, 0)
	if err != nil {
		return err
	}
	s.stopFollowing()
	_ = s.a.fillPeople(ctx, s.rt, id, s.people)
	st := sessionstate.New()
	for _, ev := range hist {
		st.Apply(ev)
	}
	s.mu.Lock()
	s.id, s.title, s.st = id, sess.Title, st
	s.rend = s.newRenderer()
	s.rend.Prime(hist)
	s.queue, s.current, s.asked = nil, nil, map[string]bool{}
	s.refreshPrompt()
	s.tracked, s.runsWanted = nil, 0
	repos := repoNames(*sess)
	fmt.Fprintln(s.con, s.sty.Bold(orStr(sess.Title, "Session"))+" "+s.sty.Dim(repos+" · "+sessionURL(s.rt.Base, id)))
	recent := hist[max(0, len(hist)-15):]
	ho := s.a.renderOpts(false, s.people)
	ho.Chat = true
	hr := render.New(s.con, ho)
	for _, ev := range recent {
		hr.Event(ev)
	}
	hr.Flush()
	for _, ap := range st.Approvals {
		s.enqueueApproval(ap)
	}
	s.askNext()
	if st.Active != nil {
		s.act.Start()
	}
	s.mu.Unlock()
	s.refreshStatus()
	s.startFollowing()
	return nil
}

// ---- sending ----

func (s *chatSession) send(text string) {
	s.mu.Lock()
	id := s.id
	mode, model := s.mode, s.model
	running := s.st.Active != nil || len(s.st.Queued) > 0
	s.mu.Unlock()
	ctx := s.ctx
	s.own.Expect(text)
	s.act.Start()
	s.refreshStatus()
	if id == "" {
		s.start(text)
		return
	}
	res, err := s.rt.Client.SubmitPrompt(ctx, id, api.PromptCommand{Text: text, Mode: mode, Model: model}, s.a.NewKey())
	if err != nil {
		s.settleActivity()
		s.fail(err)
		return
	}
	s.own.Sent(res.PromptID)
	s.mu.Lock()
	if res.PromptID != "" {
		s.tracked = append(s.tracked, res.PromptID)
	} else {
		s.runsWanted = s.st.Runs + 1
	}
	s.mu.Unlock()
	if running {
		s.dim("queued — Waniis will read it next. /steer <text> redirects the run instead.")
	}
	s.startFollowing()
}

// settleActivity stops the status line when nothing is going after a send that failed.
func (s *chatSession) settleActivity() {
	s.mu.Lock()
	if s.st.Idle() {
		s.act.Stop()
	}
	s.mu.Unlock()
	s.refreshStatus()
}

// start creates the session from the first line.
func (s *chatSession) start(text string) {
	in := api.CreateSessionInput{Prompt: &api.PromptInput{Type: "prompt.submit", Text: text, Mode: s.mode, Model: s.model, Source: "cli"}}
	if s.noRepo {
		in.Stage = "chat"
	} else {
		names, err := s.a.resolveRepos(nil)
		if err != nil {
			s.dim("No git repo here: starting a conversation with no repo.")
			in.Stage = "chat"
		} else {
			for _, n := range names {
				in.Repos = append(in.Repos, api.RepoInput{CloneURL: cloneURL(n)})
			}
		}
	}
	created, err := s.rt.Client.CreateSession(s.ctx, in, s.a.NewKey())
	if err != nil {
		s.settleActivity()
		s.fail(err)
		return
	}
	s.mu.Lock()
	s.id, s.title, s.st = created.ID, "", sessionstate.New()
	s.rend = s.newRenderer()
	s.runsWanted = 1
	s.sayLocked(s.sty.Dim("Started a session · " + sessionURL(s.rt.Base, created.ID)))
	s.mu.Unlock()
	s.startFollowing()
}

func (s *chatSession) fail(err error) {
	ee := exitcode.As(err)
	msg := s.sty.Red("Error: ") + ee.Message
	if ee.Hint != "" {
		msg += "\n  " + ee.Hint
	}
	s.say(msg)
}

// waitSettled blocks until the prompts this chat sent have ended (or the context ends).
func (s *chatSession) waitSettled() {
	for s.ctx.Err() == nil {
		s.mu.Lock()
		done := true
		for _, p := range s.tracked {
			if s.st.Outcomes[s.st.RunOf[p]] == nil && !s.st.Cancelled[p] {
				done = false
			}
		}
		if s.st.Runs < s.runsWanted {
			done = false
		}
		s.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---- approvals ----

func (s *chatSession) enqueueApproval(ap api.ApprovalStatus) {
	if s.asked[ap.RequestID] {
		return
	}
	s.asked[ap.RequestID] = true
	exp, _ := time.Parse(time.RFC3339, ap.ExpiresAt)
	switch ap.Kind {
	case "network":
		host, _, _ := strings.Cut(ap.What, " ")
		s.queue = append(s.queue, question{kind: "net", requestID: ap.RequestID, host: host, expires: exp,
			text: "Allow " + ap.What + " for this session? [y/N]"})
	default:
		s.queue = append(s.queue, question{kind: "tool", requestID: ap.RequestID, expires: exp,
			text: "Allow " + ap.What + "? [y] once, [s] for this session, [N] deny"})
	}
}

// watch reacts to events that need an answer from the person. Called with s.mu held.
func (s *chatSession) watch(ev api.Event) {
	switch ev.Type {
	case "mcp.approval_requested", "network.access_requested":
		for _, ap := range s.st.Approvals {
			s.enqueueApproval(ap)
		}
	case "sandbox.egress_blocked":
		var p struct {
			Host string `json:"host"`
		}
		if err := jsonUnmarshal(ev.Payload, &p); err == nil && p.Host != "" && !s.asked["blocked:"+p.Host] && !s.cannotAllow && !s.hasNetRequest(p.Host) {
			s.asked["blocked:"+p.Host] = true
			s.queue = append(s.queue, question{kind: "allow", host: p.Host, text: "Allow " + p.Host + " for this session (reads only)? [y/N]"})
		}
	case "mcp.approval_decided", "network.access_decided":
		var p struct {
			RequestID string `json:"requestId"`
		}
		_ = jsonUnmarshal(ev.Payload, &p)
		kept := s.queue[:0]
		for _, q := range s.queue {
			if q.requestID != p.RequestID {
				kept = append(kept, q)
			}
		}
		s.queue = kept
		if s.current != nil && s.current.requestID == p.RequestID {
			s.current = nil
			s.refreshPrompt()
			fmt.Fprintln(s.con, s.sty.Dim("(answered elsewhere)"))
		}
	}
	s.askNext()
}

func (s *chatSession) hasNetRequest(host string) bool {
	for _, ap := range s.st.Approvals {
		if ap.Kind == "network" && strings.HasPrefix(ap.What, host+" ") {
			return true
		}
	}
	return false
}

// askNext shows the next question when none is open. Called with s.mu held.
func (s *chatSession) askNext() {
	for s.current == nil && len(s.queue) > 0 {
		q := s.queue[0]
		s.queue = s.queue[1:]
		if !q.expires.IsZero() && !s.a.Now().Before(q.expires) {
			continue
		}
		s.current = &q
		s.refreshPrompt()
		s.sayLocked(s.sty.Yellow("⏸ ") + q.text)
	}
}

// answer applies the line to the open question; it reports whether the line was an answer. The
// question stays open while the decision is sent, so a refusal can drop the questions that
// would be refused too.
func (s *chatSession) answer(q *question, line string) bool {
	l := strings.ToLower(line)
	yes := l == "y" || l == "yes"
	no := l == "" || l == "n" || l == "no"
	if q.kind == "pick" {
		n, err := strconv.Atoi(l)
		s.closeQuestion()
		if err != nil || n < 1 || n > len(q.picks) {
			return false
		}
		if err := s.attach(q.picks[n-1]); err != nil {
			s.fail(err)
		}
		return true
	}
	session := q.kind == "tool" && (l == "s" || l == "session")
	if !yes && !no && !session {
		s.closeQuestion()
		s.dim("Skipped the question (it stays pending in the web app).")
		return false
	}
	switch {
	case session:
		s.report(s.rt.Client.ToolApprovalDecide(s.ctx, s.id, q.requestID, "session"), "Allowed for this session.")
	case q.kind == "tool":
		d := "deny"
		if yes {
			d = "once"
		}
		s.report(s.rt.Client.ToolApprovalDecide(s.ctx, s.id, q.requestID, d), map[bool]string{true: "Allowed once.", false: "Denied."}[yes])
	case q.kind == "net":
		s.report(s.rt.Client.NetworkDecide(s.ctx, s.id, q.requestID, yes, "session"), map[bool]string{true: "Allowed " + q.host + " for this session.", false: "Denied."}[yes])
	case q.kind == "allow":
		if !yes {
			s.dim("Left blocked.")
			break
		}
		s.report(s.rt.Client.NetworkAllow(s.ctx, s.id, q.host, "session", "read"), "Allowed "+q.host+" for this session (reads only).")
	}
	s.closeQuestion()
	return true
}

func (s *chatSession) closeQuestion() {
	s.mu.Lock()
	s.current = nil
	s.refreshPrompt()
	s.askNext()
	s.mu.Unlock()
}

// report prints the outcome of a decision; a 403 means the person's role can't decide it.
func (s *chatSession) report(err error, ok string) {
	if err == nil {
		s.dim(ok)
		return
	}
	ee := exitcode.As(err)
	if ee.Status == 403 || ee.Code == exitcode.NotFound && ee.Status == 403 {
		s.mu.Lock()
		s.cannotAllow = true
		kept := s.queue[:0]
		for _, q := range s.queue {
			if q.kind != "allow" {
				kept = append(kept, q)
			}
		}
		s.queue = kept
		s.mu.Unlock()
		s.say(s.sty.Yellow("You can't decide that here: ") + ee.Message + s.sty.Dim(" Ask a workspace admin; I won't ask again about blocked hosts."))
		return
	}
	s.fail(err)
}

// ---- slash commands ----

func (s *chatSession) needSession() bool {
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	if id == "" {
		s.dim("No session yet: type your first message to start one.")
		return false
	}
	return true
}

func (s *chatSession) slash(line string) bool {
	name, arg, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	arg = strings.TrimSpace(arg)
	cmd, ok := chat.Lookup(name)
	if !ok {
		s.say("Unknown command /" + name + ". /help lists them. (To send text that starts with a slash, put a space before it.)")
		return false
	}
	ctx := s.ctx
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	switch cmd.Name {
	case "quit":
		return true
	case "help":
		for _, l := range chat.HelpLines() {
			s.say(l)
		}
		s.dim("Enter sends a prompt (queued while a run is going). /steer redirects the run. Ctrl-C once stops following; twice leaves.")
	case "clear":
		s.con.Clear()
	case "new":
		s.stopFollowing()
		s.mu.Lock()
		s.id, s.title, s.st, s.rend = "", "", sessionstate.New(), nil
		s.act.Stop()
		s.queue, s.current = nil, nil
		s.refreshPrompt()
		s.mu.Unlock()
		if arg != "" {
			s.send(arg)
		} else {
			s.dim("New session: type your first message.")
		}
	case "sessions":
		page, err := s.rt.Client.ListSessions(ctx, api.ListFilter{Limit: 9})
		if err != nil {
			s.fail(err)
			break
		}
		items := page.All()
		if len(items) == 0 {
			s.dim("No sessions yet.")
			break
		}
		q := question{kind: "pick"}
		for i, it := range items {
			q.picks = append(q.picks, it.ID)
			s.say(fmt.Sprintf("  %d  %s %s", i+1, truncate(orStr(it.Title, "Untitled"), 50), s.sty.Dim("· "+orDash(it.Status)+" · "+repoNames(it))))
		}
		q.text = "Pick a session by number (anything else cancels)"
		s.mu.Lock()
		s.current = &q
		s.refreshPrompt()
		s.sayLocked(s.sty.Yellow("⏸ ") + q.text)
		s.mu.Unlock()
	case "switch":
		if arg == "" {
			s.say("Usage: /switch <id>")
			break
		}
		if err := s.attach(arg); err != nil {
			s.fail(err)
		}
	case "status":
		if !s.needSession() {
			break
		}
		st, _, err := s.a.fetchStatus(ctx, s.rt, id)
		if err != nil {
			s.fail(err)
			break
		}
		s.mu.Lock()
		s.sayLocked("")
		_ = s.a.writeStatus(s.rt, id, st)
		s.mu.Unlock()
	case "stop":
		if s.needSession() {
			s.report(s.rt.Client.StopRun(ctx, id), "Stop requested. What the run changed is kept.")
		}
	case "steer":
		if !s.needSession() {
			break
		}
		if arg == "" {
			s.say("Usage: /steer <text>")
			break
		}
		s.mu.Lock()
		active := s.st.Active != nil
		s.mu.Unlock()
		if !active {
			s.dim("Nothing is running to steer. Press Enter to send it as a prompt instead.")
			break
		}
		_, err := s.rt.Client.Steer(ctx, id, arg, s.a.NewKey())
		if err != nil {
			s.fail(steerError(err, id))
			break
		}
		s.dim("Steering sent.")
	case "queue":
		s.mu.Lock()
		q := append([]sessionstate.Prompt(nil), s.st.Queued...)
		s.mu.Unlock()
		if len(q) == 0 {
			s.dim("Nothing is queued.")
		}
		for i, p := range q {
			s.say(fmt.Sprintf("  %d  %s", i+1, truncate(p.Text, 100)))
		}
	case "logs":
		if !s.needSession() {
			break
		}
		n := 30
		if arg != "" {
			v, err := strconv.Atoi(arg)
			if err != nil || v < 1 {
				s.say("Usage: /logs [N]")
				break
			}
			n = v
		}
		evs, err := stream.Tail(ctx, s.rt.Client, id, n)
		if err != nil {
			s.fail(err)
			break
		}
		_ = s.a.fillPeople(ctx, s.rt, id, s.people)
		s.mu.Lock()
		lo := s.a.renderOpts(false, s.people)
		lo.Chat = true
		r := render.New(s.con, lo)
		for _, ev := range evs {
			r.Event(ev)
		}
		r.Flush()
		s.mu.Unlock()
	case "diff":
		if s.needSession() {
			s.diff(id)
		}
	case "pr":
		s.pr(id)
	case "preview":
		if s.needSession() {
			s.preview(id)
		}
	case "open":
		if s.needSession() {
			u := sessionURL(s.rt.Base, id)
			if s.a.OpenBrowser != nil && s.a.OpenBrowser(u) == nil {
				s.dim("Opened " + u)
			} else {
				s.say(u)
			}
		}
	case "share":
		if !s.needSession() {
			break
		}
		role := arg
		if role == "" {
			role = "viewer"
		}
		if err := oneOf("link", role, validRoles); err != nil {
			s.say("Usage: /share [viewer|collaborator]")
			break
		}
		l, err := s.rt.Client.SetShareLink(ctx, id, role)
		if err != nil {
			s.report(err, "")
			break
		}
		if l != nil {
			s.say(fmt.Sprintf("Share link (%s): %s", l.Role, l.URL))
		}
	case "export":
		if s.needSession() {
			s.exportRow(id, arg)
		}
	case "mode":
		if err := oneOf("mode", arg, validModes); err != nil || arg == "" {
			s.say("Usage: /mode auto|build|plan|ask")
			break
		}
		s.mu.Lock()
		s.mode = arg
		s.refreshPrompt()
		s.mu.Unlock()
		s.dim("Next prompts run in " + arg + " mode.")
	case "model":
		s.chooseModel(arg)
	case "attach":
		s.say("Not available yet: attaching files needs an upload step the CLI doesn't have. Add the file in the web app.")
	case "whoami":
		me, err := s.rt.Client.Me(ctx)
		if err != nil {
			s.fail(err)
			break
		}
		s.say(fmt.Sprintf("%s (%s) on %s", me.Name, me.Login, s.rt.Base))
	case "workspace":
		s.workspace(arg)
	case "login":
		s.say("Not available yet inside the chat: leave, run `waniis login`, then `waniis chat" + map[bool]string{true: " " + id, false: ""}[id != ""] + "`.")
	case "logout":
		c := s.a.logoutCmd()
		c.SetContext(ctx)
		if err := c.RunE(c, nil); err != nil {
			s.fail(err)
			break
		}
		return true
	}
	return false
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string { return output.Truncate(s, n) }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (s *chatSession) diff(id string) {
	d, err := s.rt.Client.Diff(s.ctx, id)
	if err != nil {
		s.fail(err)
		return
	}
	if len(d.Repos) == 0 {
		s.dim("No changes yet.")
	}
	for _, r := range d.Repos {
		adds, dels := 0, 0
		for _, f := range r.Files {
			adds, dels = adds+f.Additions, dels+f.Deletions
		}
		head := fmt.Sprintf("%s (%s): %s, +%d −%d", r.Repo, r.Source, plural(max(r.TotalFiles, len(r.Files)), "file", "files"), adds, dels)
		if r.PullRequest != nil {
			head += fmt.Sprintf(" · PR #%d %s", r.PullRequest.Number, r.PullRequest.URL)
		}
		s.say(s.sty.Bold(head))
		for i, f := range r.Files {
			if i == 15 {
				s.dim(fmt.Sprintf("  … and %d more", len(r.Files)-15))
				break
			}
			s.say(fmt.Sprintf("  %-9s %s (+%d −%d)", f.Status, f.Path, f.Additions, f.Deletions))
		}
	}
	for _, g := range d.Gaps {
		s.dim("Couldn't read the " + g.Source + " changes" + map[bool]string{true: " of " + g.Repo, false: ""}[g.Repo != ""] + ": " + strings.ReplaceAll(g.Reason, "_", " "))
	}
	s.dim("The full diff is in the web app: " + sessionURL(s.rt.Base, id))
}

func (s *chatSession) pr(id string) {
	s.mu.Lock()
	prs := append([]api.PRStatus(nil), s.st.PRs...)
	s.mu.Unlock()
	if len(prs) == 0 && id != "" {
		if sess, err := s.rt.Client.GetSession(s.ctx, id); err == nil && sess.PullRequest != nil {
			prs = []api.PRStatus{{Number: sess.PullRequest.Number, URL: sess.PullRequest.URL, State: "open"}}
		}
	}
	if len(prs) == 0 {
		s.dim("No pull request yet.")
		return
	}
	for _, p := range prs {
		s.say(fmt.Sprintf("PR #%d %s%s", p.Number, p.URL, map[bool]string{true: " (draft)", false: ""}[p.Draft]))
	}
	if s.a.OpenBrowser != nil && s.a.OpenBrowser(prs[0].URL) == nil {
		s.dim("Opened it in your browser.")
	}
}

func (s *chatSession) preview(id string) {
	v, err := s.rt.Client.Surfaces(s.ctx, id)
	if err != nil {
		s.fail(err)
		return
	}
	if !v.Previews {
		s.dim("Previews aren't set up on this deployment.")
		return
	}
	n := 0
	for _, p := range v.Ports {
		if !p.Listening {
			continue
		}
		u, err := s.rt.Client.PreviewURL(s.ctx, id, "p"+strconv.Itoa(p.Port))
		if err != nil || u == "" {
			s.dim(fmt.Sprintf("Port %d: couldn't get a link.", p.Port))
			continue
		}
		n++
		s.say(fmt.Sprintf("Port %d %s %s", p.Port, s.sty.Dim("("+orDash(p.Label)+")"), u))
	}
	if n == 0 {
		s.dim("Nothing is listening in the sandbox (state: " + v.Sandbox + ").")
		return
	}
	s.dim("These links work once and expire in about a minute; run /preview again for a new one.")
}

func (s *chatSession) exportRow(id, format string) {
	if format == "" {
		format = export.CSV
	}
	if format != export.CSV && format != export.XLSX {
		s.say("Usage: /export [csv|xlsx]")
		return
	}
	sess, err := s.rt.Client.GetSession(s.ctx, id)
	if err != nil {
		s.fail(err)
		return
	}
	table := sessionExportTable(s.rt.Base, []api.Session{*sess})
	var buf bytes.Buffer
	if format == export.CSV {
		err = export.WriteCSV(&buf, table, false)
	} else {
		err = export.WriteXLSX(&buf, table)
	}
	if err != nil {
		s.fail(err)
		return
	}
	name := "waniis-" + id + "." + format
	if err := os.WriteFile(name, buf.Bytes(), 0o644); err != nil {
		s.say("Couldn't write " + name + ": " + err.Error())
		return
	}
	s.dim("Saved " + name)
}

func (s *chatSession) workspace(arg string) {
	me, err := s.rt.Client.Me(s.ctx)
	if err != nil {
		s.fail(err)
		return
	}
	if arg == "" {
		for _, w := range me.Workspaces {
			mark := " "
			if w.ID == s.rt.Client.Workspace {
				mark = "›"
			}
			s.say(fmt.Sprintf(" %s %s  %s (%s)", mark, w.ID, w.Name, w.Kind))
		}
		return
	}
	for _, w := range me.Workspaces {
		if strings.EqualFold(w.Name, arg) || w.ID == arg {
			s.rt.Client.Workspace = w.ID
			s.dim("Using " + w.Name + " for this chat. /sessions lists its sessions.")
			return
		}
	}
	s.say("No workspace called " + arg + ". /workspace lists yours.")
}
