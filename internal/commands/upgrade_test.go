package commands

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elmkarami/waniis-cli/internal/upgrade"
)

// fakeGitHub serves a release v0.1.2 with this system's archive and its checksum.
func fakeGitHub(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	name := upgrade.ArchiveName("0.1.2", runtime.GOOS, runtime.GOARCH, upgrade.Goarm())
	var archive bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&archive)
		w, _ := zw.Create("waniis.exe")
		_, _ = w.Write([]byte("NEW BINARY"))
		_ = zw.Close()
	} else {
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "waniis", Mode: 0o755, Size: 10, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte("NEW BINARY"))
		_ = tw.Close()
		_ = gz.Close()
	}
	sum := sha256.Sum256(archive.Bytes())
	calls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest", "/releases/tags/v0.1.2":
			calls++
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.1.2", "html_url": "https://github.com/x/releases/v0.1.2",
				"assets": []any{
					map[string]any{"name": "checksums.txt", "browser_download_url": srv.URL + "/dl/checksums.txt"},
					map[string]any{"name": name, "browser_download_url": srv.URL + "/dl/archive"},
				}})
		case "/dl/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		case "/dl/archive":
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

type upgradeRig struct {
	deps Deps
	out  *bytes.Buffer
	errb *bytes.Buffer
	ran  [][]string
	exe  string
	now  time.Time
	env  map[string]string
}

func newUpgradeRig(t *testing.T, version, exe string) *upgradeRig {
	t.Helper()
	old := Version
	Version = version
	t.Cleanup(func() { Version = old })
	srv, _ := fakeGitHub(t)
	r := &upgradeRig{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, exe: exe,
		now: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), env: map[string]string{}}
	r.deps = Deps{In: strings.NewReader(""), Out: r.out, Err: r.errb,
		Getenv: func(k string) string { return r.env[k] }, HTTP: srv.Client(),
		Now: func() time.Time { return r.now }, Sleep: func(context.Context, time.Duration) error { return nil },
		ReleasesAPI: srv.URL + "/releases", ConfigDir: t.TempDir(),
		Executable: func() (string, error) { return r.exe, nil },
		RunCommand: func(_ context.Context, name string, args ...string) error {
			r.ran = append(r.ran, append([]string{name}, args...))
			return nil
		}}
	return r
}

func (r *upgradeRig) exec(args ...string) int {
	r.out.Reset()
	r.errb.Reset()
	return Execute(context.Background(), r.deps, args)
}

func TestUpgradeReplacesADownloadedBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "waniis")
	_ = os.WriteFile(exe, []byte("OLD"), 0o755)
	r := newUpgradeRig(t, "0.1.1", exe)
	eq(t, r.exec("upgrade", "--check"), 0, "check exit")
	contains(t, r.out.String(), "waniis 0.1.2 is available (you have 0.1.1)")
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Fatal("--check changes nothing")
	}
	eq(t, r.exec("upgrade"), 0, "upgrade exit")
	contains(t, r.out.String(), "Upgraded waniis 0.1.1 → 0.1.2 (checksum verified)")
	if b, _ := os.ReadFile(exe); string(b) != "NEW BINARY" {
		t.Fatalf("binary: %q", b)
	}
	eq(t, len(r.ran), 0, "no brew")
}

func TestUpgradeUsesHomebrewWhenHomebrewInstalledIt(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "Cellar", "waniis", "0.1.1", "bin", "waniis")
	r := newUpgradeRig(t, "0.1.1", exe)
	eq(t, r.exec("upgrade"), 0, "exit")
	if len(r.ran) != 2 || r.ran[0][1] != "update" || r.ran[1][1] != "upgrade" || r.ran[1][2] != "elmkarami/tap/waniis" {
		t.Fatalf("ran %v", r.ran)
	}
	if !strings.HasSuffix(r.ran[0][0], filepath.FromSlash("/bin/brew")) {
		t.Fatalf("the brew beside the install: %v", r.ran[0])
	}
	contains(t, r.out.String(), "Upgraded waniis 0.1.1 → 0.1.2")
	eq(t, r.exec("upgrade", "--version", "v0.1.2"), 2, "Homebrew can't pick a version")
}

func TestUpgradeWhenNewestAndFromSource(t *testing.T) {
	r := newUpgradeRig(t, "0.1.2", filepath.Join(t.TempDir(), "waniis"))
	eq(t, r.exec("upgrade"), 0, "exit")
	contains(t, r.out.String(), "waniis 0.1.2 is the newest version")
	r2 := newUpgradeRig(t, "dev", filepath.Join(t.TempDir(), "waniis"))
	eq(t, r2.exec("upgrade"), 2, "source build")
	contains(t, r2.errb.String(), "built from source")
	eq(t, r2.exec("upgrade", "--check", "--json"), 0, "json")
	var got map[string]any
	_ = json.Unmarshal(r2.out.Bytes(), &got)
	eq(t, got["latest"], "0.1.2", "latest")
	eq(t, got["upgradeAvailable"], false, "a source build is never behind")
}

func TestDailyNoticeInATerminalOnly(t *testing.T) {
	r := newUpgradeRig(t, "0.1.1", filepath.Join(t.TempDir(), "waniis"))
	r.deps.StdinTTY, r.deps.StdoutTTY = true, true
	r.exec("upgrade", "--check")
	if strings.Contains(r.errb.String(), "new version") {
		t.Fatal("not after upgrade itself")
	}
	r.exec("version")
	contains(t, r.errb.String(), "A new version of waniis is available: 0.1.1 → 0.1.2. Run: waniis upgrade")
	r.exec("version", "--json")
	if strings.Contains(r.errb.String(), "new version") {
		t.Fatal("not with --json")
	}
	r.env[EnvNoUpdateNotifier] = "1"
	r.exec("version")
	if strings.Contains(r.errb.String(), "new version") {
		t.Fatal("turned off")
	}
	delete(r.env, EnvNoUpdateNotifier)
	r.env["CI"] = "true"
	r.exec("version")
	if strings.Contains(r.errb.String(), "new version") {
		t.Fatal("not in CI")
	}
	delete(r.env, "CI")
	r.deps.StdoutTTY = false
	r.exec("version")
	if strings.Contains(r.errb.String(), "new version") {
		t.Fatal("not in a pipe")
	}
}

// The check runs at most once a day.
func TestDailyCheckIsCached(t *testing.T) {
	old := Version
	Version = "0.1.1"
	t.Cleanup(func() { Version = old })
	srv, calls := fakeGitHub(t)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	var errb bytes.Buffer
	d := Deps{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &errb, Getenv: func(string) string { return "" },
		HTTP: srv.Client(), Now: func() time.Time { return now }, ReleasesAPI: srv.URL + "/releases",
		ConfigDir: t.TempDir(), StdinTTY: true, StdoutTTY: true}
	for i := 0; i < 3; i++ {
		Execute(context.Background(), d, []string{"version"})
	}
	eq(t, *calls, 1, "one look a day")
	now = now.Add(25 * time.Hour)
	Execute(context.Background(), d, []string{"version"})
	eq(t, *calls, 2, "a day later")
}
