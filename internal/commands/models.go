package commands

import (
	"fmt"
	"strings"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/output"
	"github.com/spf13/cobra"
)

func (a *app) modelsCmd() *cobra.Command {
	var configured, notConfigured bool
	var search string
	c := &cobra.Command{
		Use:   "models",
		Short: "List the models this workspace knows, and which ones it can use",
		Long: "List the workspace's models: usable ones (a provider key, or one Waniis manages) first, then " +
			"the ones that can't be used yet and why. Choose one per prompt with --model on new and prompt, " +
			"or /model in the chat.",
		Example: "  waniis models\n  waniis models --configured\n  waniis models --search sonnet\n  waniis models --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			m, err := rt.Client.Models(cmd.Context())
			if err != nil {
				return err
			}
			var shown []api.Model
			for _, model := range m.Sorted() {
				switch {
				case configured && !model.Available, notConfigured && model.Available:
					continue
				case search != "" && !m.Matches(model, search):
					continue
				}
				shown = append(shown, model)
			}
			if a.useJSON() {
				rows := make([]map[string]any, 0, len(shown))
				for _, model := range shown {
					row := map[string]any{"ref": model.Ref, "title": model.Title, "provider": model.Provider,
						"tier": model.Tier, "available": model.Available, "default": model.Ref == m.Default()}
					if why := m.Why(model); why != "" {
						row["unavailableReason"] = why
					}
					rows = append(rows, row)
				}
				locked, _ := m.Locked()
				return output.JSON(a.Out, map[string]any{"default": nullable(m.Default()), "routed": m.Routed(),
					"locked": locked, "models": rows})
			}
			rows := make([][]string, 0, len(shown))
			for _, model := range shown {
				status := "ready"
				if why := m.Why(model); why != "" {
					status = why
				}
				name := model.Ref
				if model.Ref == m.Default() {
					name += " (default)"
				}
				rows = append(rows, []string{name, model.Title, m.ProviderTitle(model.Provider), strings.TrimSpace(model.Tier), status})
			}
			if err := output.Table(a.Out, []string{"MODEL", "NAME", "PROVIDER", "TIER", "STATUS"}, rows); err != nil {
				return err
			}
			if m.Routed() {
				fmt.Fprintln(a.Out, "\nThe router picks a model for each task (fast, standard or strong).")
			}
			if locked, why := m.Locked(); locked {
				fmt.Fprintln(a.Out, why)
			}
			return nil
		},
	}
	fl := c.Flags()
	fl.BoolVar(&configured, "configured", false, "only the models this workspace can use")
	fl.BoolVar(&notConfigured, "not-configured", false, "only the models it can't use yet")
	fl.StringVar(&search, "search", "", "only models matching these words")
	c.MarkFlagsMutuallyExclusive("configured", "not-configured")
	return c
}
