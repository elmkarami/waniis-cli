package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/output"
	"github.com/elmkarami/waniis-cli/internal/upgrade"
)

// EnvNoUpdateNotifier turns the daily "a new version is available" line off.
const EnvNoUpdateNotifier = "WANIIS_NO_UPDATE_NOTIFIER"

// checkEvery is how often the CLI looks for a new version by itself.
const checkEvery = 24 * time.Hour

// executable is the running binary's path, symlinks resolved.
func (a *app) executable() (string, error) {
	if a.Executable != nil {
		return a.Executable()
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func (a *app) releasesAPI() string {
	if a.ReleasesAPI != "" {
		return a.ReleasesAPI
	}
	return upgrade.ReleasesAPI
}

func (a *app) run(ctx context.Context, name string, args ...string) error {
	if a.RunCommand != nil {
		return a.RunCommand(ctx, name, args...)
	}
	c := exec.CommandContext(ctx, name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, a.Out, a.Err
	return c.Run()
}

func (a *app) upgradeCmd() *cobra.Command {
	var check bool
	var version string
	c := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade waniis to the newest version",
		Long: "Upgrade waniis to the newest release. Installed with Homebrew, it runs brew update and brew upgrade " +
			"for you (so the tap is always current). Otherwise it downloads the release for this system, checks it " +
			"against the release's checksums and replaces itself. --check only says whether there is a newer version.\n\n" +
			"waniis also looks for a new version once a day and says so in one line after a command, in a terminal " +
			"only. Set " + EnvNoUpdateNotifier + "=1 to turn that off.",
		Example: "  waniis upgrade\n  waniis upgrade --check\n  waniis upgrade --version v0.1.2",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			exe, err := a.executable()
			if err != nil {
				return fmt.Errorf("can't find the waniis binary: %w", err)
			}
			upgrade.Cleanup(exe)
			method := upgrade.Detect(exe)
			if version != "" && method == upgrade.Homebrew {
				return exitcode.Usagef("Homebrew installs only the newest version").
					WithHint("Run waniis upgrade without --version, or install a release by hand from https://github.com/elmkarami/waniis-cli/releases")
			}
			rel, err := upgrade.Latest(ctx, a.HTTP, a.releasesAPI(), version)
			if errors.Is(err, upgrade.ErrNotFound) && version != "" {
				return exitcode.Usagef("there is no release %s", version)
			}
			if err != nil {
				return fmt.Errorf("can't reach GitHub to look for a new version: %w", err)
			}
			a.saveCheck(rel.Version())
			newer := upgrade.Newer(Version, rel.Version())
			if check {
				if a.useJSON() {
					return output.JSON(a.Out, map[string]any{"current": Version, "latest": rel.Version(),
						"upgradeAvailable": newer, "method": string(method), "url": rel.URL})
				}
				switch {
				case !upgrade.IsVersion(Version):
					fmt.Fprintf(a.Out, "This is a source build (%s). The newest release is %s.\n", Version, rel.Version())
				case newer:
					fmt.Fprintf(a.Out, "waniis %s is available (you have %s). Run: waniis upgrade\n%s\n", rel.Version(), Version, rel.URL)
				default:
					fmt.Fprintf(a.Out, "waniis %s is the newest version.\n", Version)
				}
				return nil
			}
			if !upgrade.IsVersion(Version) && version == "" {
				return exitcode.Usagef("this waniis was built from source (%s), so it can't upgrade itself", Version).
					WithHint("Install a release: brew install elmkarami/tap/waniis, or go install github.com/elmkarami/waniis-cli/cmd/waniis@latest")
			}
			if version == "" && !newer {
				fmt.Fprintf(a.Out, "waniis %s is the newest version.\n", Version)
				return nil
			}
			if method == upgrade.Homebrew {
				brew := upgrade.BrewFor(exe)
				fmt.Fprintf(a.Err, "Upgrading with Homebrew: brew update, then brew upgrade %s\n", upgrade.Formula)
				if err := a.run(ctx, brew, "update", "--quiet"); err != nil {
					return fmt.Errorf("brew update failed: %w", err)
				}
				if err := a.run(ctx, brew, "upgrade", upgrade.Formula); err != nil {
					return fmt.Errorf("brew upgrade failed: %w", err)
				}
				fmt.Fprintf(a.Out, "Upgraded waniis %s → %s.\n", Version, rel.Version())
				return nil
			}
			name := upgrade.ArchiveName(rel.Version(), runtime.GOOS, runtime.GOARCH, upgrade.Goarm())
			fmt.Fprintf(a.Err, "Downloading %s…\n", name)
			dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			archive, err := upgrade.Download(dctx, a.HTTP, rel, name)
			if err != nil {
				return err
			}
			bin, err := upgrade.Extract(archive, runtime.GOOS == "windows")
			if err != nil {
				return fmt.Errorf("reading %s: %w", name, err)
			}
			if err := upgrade.Replace(exe, bin, runtime.GOOS == "windows"); err != nil {
				if errors.Is(err, os.ErrPermission) {
					return exitcode.Usagef("can't replace %s: permission denied", exe).
						WithHint("Run it again with sudo, or install waniis in a folder you own.")
				}
				return fmt.Errorf("replacing %s: %w", exe, err)
			}
			fmt.Fprintf(a.Out, "Upgraded waniis %s → %s (checksum verified).\n", Version, rel.Version())
			return nil
		},
	}
	c.Flags().BoolVar(&check, "check", false, "only say whether a newer version exists")
	c.Flags().StringVar(&version, "version", "", "install this version instead of the newest (not with Homebrew)")
	return c
}

// updateCheck is what the daily check remembers.
type updateCheck struct {
	CheckedAt time.Time `json:"checkedAt"`
	Latest    string    `json:"latest"`
}

func (a *app) checkFile() string {
	if a.ConfigDir == "" {
		return ""
	}
	return filepath.Join(a.ConfigDir, "update-check.json")
}

func (a *app) loadCheck() (updateCheck, bool) {
	var uc updateCheck
	f := a.checkFile()
	if f == "" {
		return uc, false
	}
	b, err := os.ReadFile(f)
	if err != nil || json.Unmarshal(b, &uc) != nil {
		return uc, false
	}
	return uc, true
}

func (a *app) saveCheck(latest string) {
	f := a.checkFile()
	if f == "" {
		return
	}
	b, _ := json.Marshal(updateCheck{CheckedAt: a.Now(), Latest: latest})
	_ = os.MkdirAll(filepath.Dir(f), 0o700)
	_ = os.WriteFile(f, b, 0o600)
}

// notifierOn says whether this run may look for, and mention, a new version: a person at a
// terminal, not CI, not JSON, not the MCP server or completion, and a release build.
func (a *app) notifierOn(cmd string) bool {
	switch cmd {
	case "upgrade", "mcp", "completion", "__complete", "__completeNoDesc":
		return false
	}
	return a.interactive() && !a.useJSON() && a.Getenv(EnvNoUpdateNotifier) == "" &&
		a.checkFile() != "" && upgrade.IsVersion(Version)
}

// startUpdateCheck looks for a new version in the background when the last look is over a
// day old. It never delays a command: whatever it finds is kept for the next run.
func (a *app) startUpdateCheck(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	uc, ok := a.loadCheck()
	if ok && a.Now().Sub(uc.CheckedAt) < checkEvery {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		if rel, err := upgrade.Latest(cctx, a.HTTP, a.releasesAPI(), ""); err == nil {
			a.saveCheck(rel.Version())
		}
	}()
	return done
}

// updateNotice is the one line shown after a command when a newer version is known.
func (a *app) updateNotice() string {
	uc, ok := a.loadCheck()
	if !ok || !upgrade.Newer(Version, uc.Latest) {
		return ""
	}
	return fmt.Sprintf("A new version of waniis is available: %s → %s. Run: waniis upgrade", Version, uc.Latest)
}
