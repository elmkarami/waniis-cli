package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/output"
)

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI's version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.useJSON() {
				return output.JSON(a.Out, map[string]string{"version": Version, "commit": Commit, "date": Date, "os": goos(), "arch": goarch()})
			}
			fmt.Fprintf(a.Out, "waniis %s (%s, %s/%s)\n", Version, Commit, goos(), goarch())
			return nil
		},
	}
}
