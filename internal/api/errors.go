package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elmkarami/waniis-cli/internal/exitcode"
)

// errorBody is the API's error shape: {"error": "<code>", "message": "...", "reason": "..."}.
// Validation failures add "issues". A nested {"error": {"code", "message"}} is read too.
type errorBody struct {
	Error   json.RawMessage `json:"error"`
	Message string          `json:"message"`
	Reason  string          `json:"reason"`
	Hint    string          `json:"hint"`
	Issues  []struct {
		Path    []any  `json:"path"`
		Message string `json:"message"`
	} `json:"issues"`
}

// friendly maps API error codes to messages and hints that say what to do. Where the API sends
// its own message, that wins and only the hint is added.
var friendly = map[string]struct{ message, hint string }{
	"access_pending":      {"Your access request hasn't been approved yet.", "You will be able to use Waniis once it is approved; ask your workspace admin or try again later."},
	"reason_required":     {"A reason is required for this action.", "Say why you need it and retry."},
	"billing_blocked":     {"Billing blocked this action.", "Open Settings > Billing in the web app to resolve it."},
	"model_not_allowed":   {"That model isn't allowed in this workspace.", "Omit --model or choose one the workspace allows."},
	"invalid_state":       {"The session can't do that in its current state.", ""},
	"session_deleted":     {"That session was deleted.", ""},
	"plan_needs_admin":    {"This plan needs a workspace owner or admin to approve it.", ""},
	"not_found":           {"Not found, or you don't have access to it.", "Check the id and the workspace (--workspace)."},
	"forbidden":           {"You're not allowed to do that.", "Your role in this workspace or on this session doesn't permit it."},
	"sharing_unavailable": {"Sharing isn't available right now.", "Try again in a moment."},
	"attachment_refused":  {"An attachment was refused.", ""},
	"insufficient_scope":  {"This token doesn't have the scope this command needs.", "Create a token with more scopes, or run `waniis login`."},
	"missing_scope":       {"This token doesn't have the scope this command needs.", "Create a token with more scopes, or run `waniis login`."},
	"token_expired":       {"Your token expired.", "Run `waniis login`."},
	"token_revoked":       {"Your token was revoked.", "Run `waniis login`."},
	"unauthorized":        {"You're not signed in.", "Run `waniis login`."},
	"rate_limited":        {"Too many requests.", "Wait a moment and retry."},
}

// MapError turns an HTTP error response into a typed error with the right exit code.
func MapError(status int, body []byte, requestID string) *exitcode.Error {
	var eb errorBody
	_ = json.Unmarshal(body, &eb)
	code := ""
	if len(eb.Error) > 0 {
		var s string
		if json.Unmarshal(eb.Error, &s) == nil {
			code = s
		} else {
			var nested struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(eb.Error, &nested) == nil {
				code = nested.Code
				if eb.Message == "" {
					eb.Message = nested.Message
				}
			}
		}
	}

	exit := exitFor(status, code)
	msg, hint := eb.Message, eb.Hint
	if f, ok := friendly[code]; ok {
		if msg == "" {
			msg = f.message
		}
		if hint == "" {
			hint = f.hint
		}
		// The two messages the CLI owns because the right action is a CLI one.
		if code == "access_pending" {
			msg, hint = f.message, f.hint
		}
	}
	if status == 401 {
		if msg == "" || code == "" {
			msg = "You're not signed in, or your token expired."
		}
		hint = "Run `waniis login`."
	}
	if msg == "" {
		msg = defaultMessage(status)
	}
	if code == "invalid_body" && len(eb.Issues) > 0 {
		var parts []string
		for _, is := range eb.Issues {
			p := make([]string, 0, len(is.Path))
			for _, seg := range is.Path {
				p = append(p, fmt.Sprint(seg))
			}
			parts = append(parts, strings.TrimSpace(strings.Join(p, ".")+" "+is.Message))
		}
		msg = "The request was invalid: " + strings.Join(parts, "; ")
	}
	if eb.Reason != "" && !strings.Contains(msg, eb.Reason) {
		msg += " (" + eb.Reason + ")"
	}
	if code == "" {
		code = fmt.Sprintf("http_%d", status)
	}
	return &exitcode.Error{Code: exit, APICode: code, Message: msg, Hint: hint, Status: status, RequestID: requestID}
}

// exitFor maps an HTTP status (and the API's code) to the CLI's exit codes (FR-17). The API
// answers 404 for "not found" and "not yours" alike, on purpose.
func exitFor(status int, code string) int {
	switch code {
	case "access_pending", "insufficient_scope", "missing_scope", "token_expired", "token_revoked", "unauthorized":
		return exitcode.Auth
	case "billing_blocked":
		return exitcode.Billing
	}
	switch status {
	case 401:
		return exitcode.Auth
	case 402:
		return exitcode.Billing
	case 403, 404, 410:
		return exitcode.NotFound
	case 409, 412:
		return exitcode.Conflict
	case 429, 502, 503, 504:
		return exitcode.Unavailable
	}
	return exitcode.General
}

func defaultMessage(status int) string {
	switch {
	case status >= 500:
		return fmt.Sprintf("Waniis had a problem (HTTP %d).", status)
	default:
		return fmt.Sprintf("The request failed (HTTP %d).", status)
	}
}
