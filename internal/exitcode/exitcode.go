// Package exitcode holds the CLI's stable exit codes (CLT-15/FR-17) and the typed error that
// carries one, so every layer can say what went wrong without knowing how the process exits.
package exitcode

import (
	"errors"
	"fmt"
)

// Stable exit codes. They are part of the CLI's public contract: scripts and agents branch on them.
const (
	OK          = 0
	General     = 1
	Usage       = 2
	Auth        = 3
	NotFound    = 4
	Conflict    = 5
	Billing     = 6
	Unavailable = 7
	Timeout     = 8
	Partial     = 9
	RunFailed   = 10
	Interrupted = 130
)

// Error is an expected failure: what failed, a stable machine code, a hint for what to do next,
// and the exit code the process ends with.
type Error struct {
	Code      int    // exit code
	APICode   string // the API's error code (or a CLI code such as "confirmation_required")
	Message   string
	Hint      string
	Status    int // HTTP status, 0 when none
	RequestID string
	Cause     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

// New builds an Error with an exit code and a message.
func New(code int, apiCode, message string) *Error {
	return &Error{Code: code, APICode: apiCode, Message: message}
}

// Usagef is a usage error (exit 2): bad flags or arguments.
func Usagef(format string, args ...any) *Error {
	return &Error{Code: Usage, APICode: "usage", Message: fmt.Sprintf(format, args...)}
}

// WithHint returns the error with a hint attached.
func (e *Error) WithHint(hint string) *Error { e.Hint = hint; return e }

// Of returns the exit code for any error: the typed code, or General for anything else.
func Of(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return General
}

// As extracts the typed error, wrapping a plain error as General.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: General, APICode: "error", Message: err.Error(), Cause: err}
}
