package config

import (
	"path/filepath"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	ok := map[string]string{
		"https://app.waniis.com":   "https://app.waniis.com",
		"https://app.waniis.com/":  "https://app.waniis.com",
		"http://localhost:8787/":   "http://localhost:8787",
		"http://127.0.0.1:5173":    "http://127.0.0.1:5173",
		"https://api.waniis.com/x": "https://api.waniis.com/x",
	}
	for in, want := range ok {
		got, err := NormalizeBaseURL(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"http://example.com", "ftp://x", "not a url", "", "https://"} {
		if _, err := NormalizeBaseURL(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestPickPrecedence(t *testing.T) {
	if Pick("flag", "env", "def") != "flag" || Pick("", "env", "def") != "env" || Pick("", "", "def") != "def" {
		t.Fatal("flag, then env, then default")
	}
}

func TestAppURL(t *testing.T) {
	if AppURL("https://api.waniis.com") != "https://app.waniis.com" || AppURL("https://app.waniis.com/") != "https://app.waniis.com" {
		t.Fatal("api alias maps to app")
	}
}

func TestDir(t *testing.T) {
	env := map[string]string{"WANIIS_CONFIG_DIR": "/x/y"}
	if d, _ := Dir(func(k string) string { return env[k] }); d != "/x/y" {
		t.Fatal(d)
	}
	env = map[string]string{"XDG_CONFIG_HOME": "/xdg"}
	if d, _ := Dir(func(k string) string { return env[k] }); d != filepath.Join("/xdg", "waniis") {
		t.Fatal(d)
	}
}
