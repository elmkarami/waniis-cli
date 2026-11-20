package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		cur, latest string
		want        bool
	}{
		{"0.1.1", "0.1.2", true}, {"0.1.2", "v0.1.2", false}, {"0.2.0", "0.1.9", false},
		{"0.9.9", "1.0.0", true}, {"1.0.0-rc.1", "1.0.0", true}, {"1.0.0", "1.0.0-rc.2", false},
		{"1.0.0-rc.1", "1.0.0-rc.2", true}, {"dev", "9.9.9", false}, {"0.1.1", "garbage", false},
	} {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.cur, c.latest, got)
		}
	}
}

func TestArchiveNameMatchesGoReleaser(t *testing.T) {
	for _, c := range [][5]string{
		{"0.1.2", "darwin", "arm64", "", "waniis_0.1.2_darwin_arm64.tar.gz"},
		{"v0.1.2", "linux", "arm", "7", "waniis_0.1.2_linux_armv7.tar.gz"},
		{"0.1.2", "windows", "amd64", "", "waniis_0.1.2_windows_amd64.zip"},
	} {
		if got := ArchiveName(c[0], c[1], c[2], c[3]); got != c[4] {
			t.Errorf("%v: got %s", c, got)
		}
	}
}

func TestDetect(t *testing.T) {
	if Detect("/opt/homebrew/Cellar/waniis/0.1.1/bin/waniis") != Homebrew || Detect("/usr/local/bin/waniis") != Binary {
		t.Fatal("detect")
	}
	if BrewFor("/opt/homebrew/Cellar/waniis/0.1.1/bin/waniis") != filepath.FromSlash("/opt/homebrew/bin/brew") {
		t.Fatal(BrewFor("/opt/homebrew/Cellar/waniis/0.1.1/bin/waniis"))
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

func TestExtract(t *testing.T) {
	bin, err := Extract(tarGz(t, map[string]string{"README.md": "x", "waniis": "BINARY"}), false)
	if err != nil || string(bin) != "BINARY" {
		t.Fatalf("%q %v", bin, err)
	}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("waniis.exe")
	_, _ = w.Write([]byte("EXE"))
	_ = zw.Close()
	if bin, err := Extract(zb.Bytes(), true); err != nil || string(bin) != "EXE" {
		t.Fatalf("%q %v", bin, err)
	}
	if _, err := Extract(tarGz(t, map[string]string{"other": "x"}), false); err == nil {
		t.Fatal("no binary")
	}
}

func TestReplace(t *testing.T) {
	for _, windows := range []bool{false, true} {
		exe := filepath.Join(t.TempDir(), "waniis")
		_ = os.WriteFile(exe, []byte("old"), 0o755)
		if err := Replace(exe, []byte("new"), windows); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(exe); string(b) != "new" {
			t.Fatalf("windows=%v: %q", windows, b)
		}
		// Windows has no executable bit.
		if info, _ := os.Stat(exe); runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
			t.Fatal("stays executable")
		}
		Cleanup(exe)
		if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
			t.Fatal("cleanup")
		}
	}
}

func TestDownloadChecksChecksums(t *testing.T) {
	archive := tarGz(t, map[string]string{"waniis": "BIN"})
	sum := sha256.Sum256(archive)
	good := hex.EncodeToString(sum[:]) + "  waniis_0.1.2_linux_amd64.tar.gz\n"
	sums := good
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checksums.txt":
			_, _ = w.Write([]byte(sums))
		case "/a.tar.gz":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	rel := &Release{Tag: "v0.1.2", Assets: []Asset{
		{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"},
		{Name: "waniis_0.1.2_linux_amd64.tar.gz", URL: srv.URL + "/a.tar.gz"},
	}}
	if data, err := Download(context.Background(), srv.Client(), rel, "waniis_0.1.2_linux_amd64.tar.gz"); err != nil || !bytes.Equal(data, archive) {
		t.Fatalf("good: %v", err)
	}
	sums = strings.Repeat("0", 64) + "  waniis_0.1.2_linux_amd64.tar.gz\n"
	if _, err := Download(context.Background(), srv.Client(), rel, "waniis_0.1.2_linux_amd64.tar.gz"); err == nil || !strings.Contains(err.Error(), "doesn't match its checksum") {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := Download(context.Background(), srv.Client(), rel, "waniis_0.1.2_plan9_amd64.tar.gz"); err == nil || !strings.Contains(err.Error(), "no build for this system") {
		t.Fatalf("missing: %v", err)
	}
}
