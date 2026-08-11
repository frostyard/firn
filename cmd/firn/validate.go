package firn

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frostyard/firn/internal/recipe"
)

func newValidateCmd() *cobra.Command {
	var probes probeFlags
	cmd := &cobra.Command{
		Use:   "validate <recipe.toml>",
		Short: "Validate a recipe against this machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			env := recipe.Env{ZoneinfoDir: "/usr/share/zoneinfo"}
			var err error
			if env.SecureBoot, env.TPM, _, err = probes.resolve(); err != nil {
				return err
			}
			l, err := recipe.Load(args[0])
			if err != nil {
				return err
			}
			if issues := recipe.Validate(l, env); len(issues) > 0 {
				for _, is := range issues {
					fmt.Fprintf(os.Stderr, "%v\n", is)
				}
				return fmt.Errorf("recipe is invalid (%d issue(s))", len(issues))
			}
			fmt.Printf("firn: recipe is valid (family %s)\n", l.Recipe.Image.Family)
			return nil
		},
	}
	probes.register(cmd, false)
	return cmd
}
