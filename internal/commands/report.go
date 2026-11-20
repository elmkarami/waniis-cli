package commands

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"

	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/output"
)

func goos() string   { return runtime.GOOS }
func goarch() string { return runtime.GOARCH }

// reportError writes err to stderr (never stdout, so data and errors don't mix) and returns the
// exit code. With --json it writes {"error": {code, message, hint, status, requestId}}.
func (a *app) reportError(ctx context.Context, args []string, err error) int {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		fmt.Fprintln(a.Err, "Interrupted.")
		return exitcode.Interrupted
	}
	var ee *exitcode.Error
	if !errors.As(err, &ee) {
		// Cobra's own failures (unknown command or flag, wrong argument count) come before any
		// command code runs: they are usage errors.
		if !a.started {
			ee = exitcode.Usagef("%s", err.Error()).WithHint("Run `waniis --help` for the commands.")
		} else {
			ee = exitcode.As(err)
		}
	}
	if a.g.JSON || slices.Contains(args, "--json") {
		type body struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Hint      string `json:"hint,omitempty"`
			Status    int    `json:"status,omitempty"`
			RequestID string `json:"requestId,omitempty"`
		}
		_ = output.JSON(a.Err, map[string]body{"error": {ee.APICode, ee.Message, ee.Hint, ee.Status, ee.RequestID}})
		return ee.Code
	}
	prefix := "Error: "
	if !a.noColor() && a.StdoutTTY {
		prefix = "\x1b[31mError:\x1b[0m "
	}
	fmt.Fprintf(a.Err, "%s%s\n", prefix, ee.Message)
	if ee.Hint != "" {
		fmt.Fprintf(a.Err, "  %s\n", ee.Hint)
	}
	if ee.RequestID != "" {
		fmt.Fprintf(a.Err, "  request id: %s\n", ee.RequestID)
	}
	return ee.Code
}

func (a *app) noColor() bool {
	return a.g.NoColor || a.Getenv("NO_COLOR") != ""
}
