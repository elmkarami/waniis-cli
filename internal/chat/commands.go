// Package chat is the terminal side of the interactive mode: the slash-command table and its
// menu, a small line editor with key decoding, and a console that keeps the input line intact
// while session events print above it. It knows nothing about the API.
package chat

import "strings"

// Command is a slash command: its name, the argument hint and a one-line description.
type Command struct {
	Name    string
	Args    string
	Desc    string
	Aliases []string
}

// Commands are all the slash commands, in the order the menu shows them.
var Commands = []Command{
	{Name: "new", Args: "[prompt]", Desc: "Start a new session from this repo"},
	{Name: "sessions", Desc: "List recent sessions and pick one by number"},
	{Name: "switch", Args: "<id>", Desc: "Follow another session"},
	{Name: "status", Desc: "Run, queue, last outcome, pull request"},
	{Name: "stop", Desc: "Stop the running work (changes are kept)"},
	{Name: "steer", Args: "<text>", Desc: "Redirect the run that is going"},
	{Name: "queue", Desc: "Show the prompts waiting behind the run"},
	{Name: "logs", Args: "[N]", Desc: "Show the last N events (default 30)"},
	{Name: "diff", Desc: "What the session changed, per repo"},
	{Name: "pr", Desc: "Show and open the pull request"},
	{Name: "preview", Desc: "Print preview links (one-time, 60 seconds)"},
	{Name: "open", Desc: "Open the session in the browser"},
	{Name: "share", Args: "[viewer|collaborator]", Desc: "Turn on a share link and print it"},
	{Name: "export", Args: "[csv|xlsx]", Desc: "Save this session's summary row to a file"},
	{Name: "mode", Args: "auto|build|plan|ask", Desc: "How the next prompts run"},
	{Name: "model", Args: "<id>", Desc: "Model for the next prompts"},
	{Name: "attach", Args: "<file>", Desc: "Attach a file to the next prompt"},
	{Name: "whoami", Desc: "Who you are and where"},
	{Name: "workspace", Args: "[name]", Desc: "List workspaces, or switch for this chat"},
	{Name: "login", Desc: "Sign in"},
	{Name: "logout", Desc: "Sign out and leave"},
	{Name: "clear", Desc: "Clear the screen (the session keeps going)"},
	{Name: "help", Aliases: []string{"?"}, Desc: "List every command"},
	{Name: "quit", Aliases: []string{"exit"}, Desc: "Leave; the session keeps running"},
}

// Lookup finds a command by name or alias (without the slash, case-insensitive).
func Lookup(name string) (Command, bool) {
	name = strings.ToLower(name)
	for _, c := range Commands {
		if c.Name == name {
			return c, true
		}
		for _, a := range c.Aliases {
			if a == name {
				return c, true
			}
		}
	}
	return Command{}, false
}

// Filter returns the commands that match what has been typed after the slash: names that start
// with it first, then names or descriptions that contain it. "/" alone returns everything. It
// returns nothing once the input has a space (the name is done) or doesn't start with "/".
func Filter(input string) []Command {
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t") {
		return nil
	}
	q := strings.ToLower(strings.TrimPrefix(input, "/"))
	var prefix, contains []Command
	for _, c := range Commands {
		names := append([]string{c.Name}, c.Aliases...)
		switch {
		case hasPrefixAny(names, q):
			prefix = append(prefix, c)
		case q != "" && (strings.Contains(c.Name, q) || strings.Contains(strings.ToLower(c.Desc), q)):
			contains = append(contains, c)
		}
	}
	return append(prefix, contains...)
}

func hasPrefixAny(names []string, q string) bool {
	for _, n := range names {
		if strings.HasPrefix(n, q) {
			return true
		}
	}
	return false
}

// Complete is the text Tab puts in the input for a command: "/name", plus a space when it takes
// arguments.
func Complete(c Command) string {
	if c.Args != "" {
		return "/" + c.Name + " "
	}
	return "/" + c.Name
}

// HelpLines lists every command with its description, aligned.
func HelpLines() []string {
	width := 0
	label := func(c Command) string { return strings.TrimSpace("/" + c.Name + " " + c.Args) }
	for _, c := range Commands {
		if n := len(label(c)); n > width {
			width = n
		}
	}
	var out []string
	for _, c := range Commands {
		l := label(c)
		out = append(out, "  "+l+strings.Repeat(" ", width-len(l)+2)+c.Desc)
	}
	return out
}
