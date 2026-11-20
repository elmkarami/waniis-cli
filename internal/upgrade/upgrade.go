// Package upgrade finds the newest waniis release on GitHub and installs it: through Homebrew
// when Homebrew installed this copy, otherwise by downloading the archive for this system,
// checking it against the release's checksums.txt and replacing the running binary.
package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

// ReleasesAPI is the GitHub API for this repository's releases.
const ReleasesAPI = "https://api.github.com/repos/elmkarami/waniis-cli/releases"

// Formula is the Homebrew formula's full name.
const Formula = "elmkarami/tap/waniis"

// maxDownload caps an archive's size.
const maxDownload = 100 << 20

// Asset is a file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Release is a GitHub release.
type Release struct {
	Tag    string  `json:"tag_name"`
	URL    string  `json:"html_url"`
	Assets []Asset `json:"assets"`
}

// Version is the release's version without the "v".
func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

func (r *Release) asset(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

func get(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "waniis-cli")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("the download is larger than expected")
	}
	return body, nil
}

// ErrNotFound is a release or file that doesn't exist.
var ErrNotFound = errors.New("not found")

// Latest is the newest release (not a pre-release). With tag, that release instead.
func Latest(ctx context.Context, client *http.Client, api, tag string) (*Release, error) {
	url := api + "/latest"
	if tag != "" {
		url = api + "/tags/v" + strings.TrimPrefix(tag, "v")
	}
	body, err := get(ctx, client, url, 4<<20)
	if err != nil {
		return nil, err
	}
	var r Release
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("reading the release: %w", err)
	}
	return &r, nil
}

// parse reads "1.2.3" or "1.2.3-rc.1" into numbers and a pre-release tag.
func parse(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, pre, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nums, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, "", false
		}
		nums[i] = n
	}
	return nums, pre, true
}

// Newer says whether latest is a later version than current. A current that isn't a version
// (a source build, "dev") is never older.
func Newer(current, latest string) bool {
	a, apre, ok1 := parse(current)
	b, bpre, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return b[i] > a[i]
		}
	}
	// 1.2.3-rc.1 comes before 1.2.3.
	switch {
	case apre != "" && bpre == "":
		return true
	case apre == "" || bpre == "":
		return false
	}
	return bpre > apre
}

// IsVersion says whether v is a release version (not "dev").
func IsVersion(v string) bool { _, _, ok := parse(v); return ok }

// Goarm is the ARM version this binary was built for ("6", "7"), "" elsewhere.
func Goarm() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "GOARM" && s.Value != "" {
			return s.Value[:1]
		}
	}
	return ""
}

// ArchiveName is the release archive for a system, as GoReleaser names it.
func ArchiveName(version, goos, goarch, goarm string) string {
	name := "waniis_" + strings.TrimPrefix(version, "v") + "_" + goos + "_" + goarch
	if goarch == "arm" && goarm != "" {
		name += "v" + goarm
	}
	if goos == "windows" {
		return name + ".zip"
	}
	return name + ".tar.gz"
}

// Method is how this copy was installed.
type Method string

const (
	Homebrew Method = "homebrew"
	Binary   Method = "binary"
)

// Detect says how the binary at exe (symlinks resolved) was installed.
func Detect(exe string) Method {
	if strings.Contains(filepath.ToSlash(exe), "/Cellar/waniis/") {
		return Homebrew
	}
	return Binary
}

// BrewFor is the brew command next to a Homebrew install (…/Cellar/waniis/… → …/bin/brew).
func BrewFor(exe string) string {
	p := filepath.ToSlash(exe)
	if i := strings.Index(p, "/Cellar/"); i >= 0 {
		return filepath.FromSlash(p[:i] + "/bin/brew")
	}
	return "brew"
}

// Download fetches this system's archive of r and checks it against checksums.txt.
func Download(ctx context.Context, client *http.Client, r *Release, name string) ([]byte, error) {
	a := r.asset(name)
	if a == nil {
		return nil, fmt.Errorf("release %s has no build for this system (%s)", r.Tag, name)
	}
	sums := r.asset("checksums.txt")
	if sums == nil {
		return nil, fmt.Errorf("release %s has no checksums.txt", r.Tag)
	}
	list, err := get(ctx, client, sums.URL, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("downloading checksums.txt: %w", err)
	}
	want := ""
	for _, line := range strings.Split(string(list), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return nil, fmt.Errorf("checksums.txt doesn't list %s", name)
	}
	data, err := get(ctx, client, a.URL, maxDownload)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s doesn't match its checksum (got %s, want %s); nothing was changed", name, got, want)
	}
	return data, nil
}

// Extract takes the waniis binary out of an archive (.tar.gz, or .zip on Windows).
func Extract(archive []byte, zipped bool) ([]byte, error) {
	if zipped {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == "waniis.exe" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer func() { _ = rc.Close() }()
				return io.ReadAll(io.LimitReader(rc, maxDownload))
			}
		}
		return nil, errors.New("the archive has no waniis.exe")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the archive has no waniis binary")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "waniis" {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// Replace puts bin in place of the executable at exe. The new file is written next to it and
// renamed over it, so a failure leaves the old one working. On Windows, where a running program
// can't be overwritten, the old one is moved aside to exe.old first (removed by Cleanup later).
func Replace(exe string, bin []byte, windows bool) error {
	info, err := os.Stat(exe)
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, info.Mode().Perm()|0o100); err != nil {
		return err
	}
	if windows {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Cleanup removes what a Windows upgrade left behind.
func Cleanup(exe string) { _ = os.Remove(exe + ".old") }
