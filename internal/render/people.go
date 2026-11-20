package render

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/elmkarami/waniis-cli/internal/api"
)

// People says who is who in a session, so events read "Sara" and never "usr_01…". The signed-in
// person reads "you". Someone it doesn't know reads "someone". It is safe for concurrent use.
type People struct {
	mu    sync.RWMutex
	self  string
	names map[string]string
}

// NewPeople returns a resolver for the signed-in person's id (may be empty) and known names.
func NewPeople(self string, names map[string]string) *People {
	p := &People{self: self, names: map[string]string{}}
	p.Merge(names)
	return p
}

// idLike matches the API's prefixed ids (usr_…, ses_…, run_…, prm_…): a prefix of two to four
// letters, an underscore and a long run of letters and digits.
var idLike = regexp.MustCompile(`^[a-z]{2,4}_[0-9a-z]{10,}$`)

// LooksLikeID reports whether s is one of the API's ids. The people route answers with the id as
// the name when it knows nothing better, and that must never be shown.
func LooksLikeID(s string) bool { return idLike.MatchString(strings.TrimSpace(s)) }

// SetSelf records who the signed-in person is.
func (p *People) SetSelf(id string) {
	p.mu.Lock()
	p.self = id
	p.mu.Unlock()
}

// Self is the signed-in person's id, "" when unknown.
func (p *People) Self() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.self
}

// Merge adds names; an empty or id-like name is ignored.
func (p *People) Merge(names map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, n := range names {
		if n = strings.TrimSpace(n); n != "" && !LooksLikeID(n) {
			p.names[id] = n
		}
	}
}

// FromPerson is the name to show for one entry of the people route: the display name, else the
// GitHub login, never an id.
func FromPerson(name, login string) string {
	if n := strings.TrimSpace(name); n != "" && !LooksLikeID(n) {
		return n
	}
	if l := strings.TrimSpace(login); l != "" && !LooksLikeID(l) {
		return l
	}
	return ""
}

// Known reports whether id can be named: "you", Waniis or someone with a name.
func (p *People) Known(id string) bool {
	switch id {
	case "", "rafiiq", "waniis":
		return true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return id == p.self || p.names[id] != ""
}

// Name is what to call id.
func (p *People) Name(id string) string {
	switch id {
	case "":
		return "someone"
	case "rafiiq", "waniis":
		return "Waniis"
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if id != "" && id == p.self {
		return "you"
	}
	if n := p.names[id]; n != "" {
		return n
	}
	return "someone"
}

// personKeys are the payload fields that hold a user id.
var personKeys = []string{"authorId", "userId", "by", "requestedBy", "editedBy", "approvedBy", "deletedBy", "to", "setBy"}

// Unknown lists the user ids an event mentions that People can't name yet, so a caller can
// refresh the session's people before showing the event.
func (p *People) Unknown(ev api.Event) []string {
	var m map[string]any
	if json.Unmarshal(ev.Payload, &m) != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, k := range personKeys {
		if id, ok := m[k].(string); ok && !seen[id] && !p.Known(id) {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Own recognizes the events that echo what this chat just sent, so a message you typed is shown
// once, as you typed it, and not again when the server repeats it. A prompt is matched by its id
// (from the command's reply) or by its text, its author and how recently it was sent.
type Own struct {
	mu      sync.Mutex
	now     func() time.Time
	ids     map[string]bool
	pending []*ownMsg
}

type ownMsg struct {
	text string
	at   time.Time
	seen map[string]bool
}

const (
	ownTTL = 2 * time.Minute
	ownMax = 50
)

// NewOwn returns an empty tracker; now is the injected clock (nil: nothing expires).
func NewOwn(now func() time.Time) *Own { return &Own{now: now, ids: map[string]bool{}} }

func (o *Own) clock() time.Time {
	if o.now == nil {
		return time.Time{}
	}
	return o.now()
}

// Expect records that text is about to be sent.
func (o *Own) Expect(text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pending = append(o.pending, &ownMsg{text: strings.TrimSpace(text), at: o.clock(), seen: map[string]bool{}})
	if len(o.pending) > ownMax {
		o.pending = o.pending[len(o.pending)-ownMax:]
	}
}

// Sent records the prompt id the server answered with.
func (o *Own) Sent(promptID string) {
	if promptID == "" {
		return
	}
	o.mu.Lock()
	o.ids[promptID] = true
	o.mu.Unlock()
}

// Match reports whether the event (its type, author, prompt id and text) echoes something this
// chat sent. self is the signed-in person's id, "" when unknown (then any author matches).
func (o *Own) Match(typ, author, self, promptID, text string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if typ == "prompt.queued" && promptID != "" && o.ids[promptID] {
		return true
	}
	if self != "" && author != self {
		return false
	}
	now := o.clock()
	kept := o.pending[:0]
	for _, m := range o.pending {
		if o.now == nil || now.Sub(m.at) <= ownTTL {
			kept = append(kept, m)
		}
	}
	o.pending = kept
	text = strings.TrimSpace(text)
	for _, m := range o.pending {
		if m.text == text && !m.seen[typ] {
			m.seen[typ] = true
			if typ == "prompt.queued" && promptID != "" {
				o.ids[promptID] = true
			}
			return true
		}
	}
	return false
}
