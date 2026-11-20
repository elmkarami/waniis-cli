// Command waniis is the Waniis command-line tool.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/cli/browser"

	"github.com/elmkarami/waniis-cli/internal/auth"
	"github.com/elmkarami/waniis-cli/internal/commands"
	"github.com/elmkarami/waniis-cli/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir, err := config.Dir(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: can't find a place for the CLI's files:", err)
		os.Exit(1)
	}
	store := &auth.ChainStore{
		Primary:  auth.KeyringStore{},
		Fallback: auth.NewFileStore(dir),
		Notify:   func(msg string) { fmt.Fprintln(os.Stderr, msg) },
	}
	deps := commands.DefaultDeps(store)
	deps.ConfigDir = dir
	deps.StdinTTY = term.IsTerminal(int(os.Stdin.Fd()))
	deps.StdoutTTY = term.IsTerminal(int(os.Stdout.Fd()))
	deps.OpenBrowser = browser.OpenURL
	deps.MakeRaw = func() (func(), error) {
		fd := int(os.Stdin.Fd())
		old, err := term.MakeRaw(fd)
		if err != nil {
			return nil, err
		}
		return func() { _ = term.Restore(fd, old) }, nil
	}
	deps.TermWidth = func() int {
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			return w
		}
		return 0
	}

	code := commands.Execute(ctx, deps, os.Args[1:])
	stop()
	os.Exit(code)
}
