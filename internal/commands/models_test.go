package commands

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestModelsListsUsableFirstAndWhyNot(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, out, _ := h.run("models")
	eq(t, code, 0, "exit")
	contains(t, out, "anthropic/claude-x (default)")
	contains(t, out, "no OpenAI key")
	if strings.Index(out, "claude-y") > strings.Index(out, "gpt-z") {
		t.Fatalf("usable models come first:\n%s", out)
	}
	_, out, _ = h.run("models", "--not-configured")
	if strings.Contains(out, "claude") || !strings.Contains(out, "gpt-z") {
		t.Fatalf("--not-configured:\n%s", out)
	}
	_, out, _ = h.run("models", "--search", "fast")
	if !strings.Contains(out, "claude-y") || strings.Contains(out, "claude-x") {
		t.Fatalf("--search:\n%s", out)
	}
	_, out, _ = h.run("models", "--configured", "--json")
	var got struct {
		Default string `json:"default"`
		Models  []struct {
			Ref       string `json:"ref"`
			Available bool   `json:"available"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	eq(t, got.Default, "anthropic/claude-x", "default")
	eq(t, len(got.Models), 2, "configured models")
}

func TestChatModelPickerTabsSearchAndChoose(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedChat(h)
	c := h.startChat(true, "chat", "ses_a")
	c.waitFor(t, "Fix checkout")
	c.send("/model\r")
	c.waitFor(t, "Not configured") // the tab bar
	c.waitFor(t, "no OpenAI key")  // a model without a key is listed, with why
	c.send("\x1b[C\x1b[C")         // → → the "Not configured" tab
	c.send("\r")                   // choosing it explains instead
	c.waitFor(t, "GPT Z can't be used yet: no OpenAI key. Add it in Settings › Models")
	c.send("/model\r")
	c.waitForAfter(t, "Settings › Models", "Not configured") // opened again, on All
	c.send("fast")                                           // typing jumps to Search
	c.waitFor(t, "Search:\x1b[0m fast")
	c.send("\r")
	c.waitFor(t, "Next prompts use Claude Y (anthropic/claude-y)")
	c.send("/model\r")
	c.waitForAfter(t, "Next prompts use Claude Y", "Not configured")
	c.send("\x1b") // Esc closes it, nothing changes
	c.send("again\r")
	var cmds []map[string]any
	for deadline := time.Now().Add(5 * time.Second); len(cmds) == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		cmds = h.f.commandsOf("prompt.submit")
	}
	c.send("\x03\x03\x03")
	c.finish(t)
	if len(cmds) == 0 {
		t.Fatal("no prompt sent")
	}
	eq(t, cmds[len(cmds)-1]["model"], "anthropic/claude-y", "model")
}

// waitForAfter waits for sub to appear after the last mark.
func (c *chatRun) waitForAfter(t *testing.T, mark, sub string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out := c.out.String()
		if i := strings.LastIndex(out, mark); i >= 0 && strings.Contains(out[i:], sub) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q after %q in:\n%s", sub, mark, c.out.String())
}

func TestLockedWorkspaceKeepsTheRoutersChoice(t *testing.T) {
	old := fakeModels
	locked := map[string]any{}
	for k, v := range fakeModels {
		locked[k] = v
	}
	locked["routing"] = map[string]any{"mode": "auto"}
	locked["modelChoice"] = map[string]any{"overrides": "locked", "message": "Your workspace's admins chose the models; the router picks one for each task."}
	fakeModels = locked
	t.Cleanup(func() { fakeModels = old })

	h := newHarness(t)
	h.signIn()
	_, out, _ := h.run("models")
	contains(t, out, "The router picks a model for each task")
	contains(t, out, "admins chose the models")

	seedChat(h)
	c := h.startChat(true, "chat", "ses_a")
	c.waitFor(t, "model chosen by task") // the welcome
	c.send("/model claude-y\r")
	c.waitFor(t, "Your workspace's admins chose the models")
	c.send("again\r")
	var cmds []map[string]any
	for deadline := time.Now().Add(5 * time.Second); len(cmds) == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		cmds = h.f.commandsOf("prompt.submit")
	}
	c.send("\x03\x03\x03")
	c.finish(t)
	if len(cmds) == 0 {
		t.Fatal("no prompt sent")
	}
	if _, ok := cmds[len(cmds)-1]["model"]; ok {
		t.Fatalf("no model is sent while locked: %v", cmds[len(cmds)-1])
	}
}
