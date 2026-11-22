package cli

import (
	"fmt"

	"github.com/Okabe-Junya/cft/schema"
	"github.com/spf13/cobra"
)

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for token spec files",
		Long: "Print the JSON Schema describing token spec YAML to stdout. " +
			"Write it to a file and point yaml-language-server at it " +
			"(modeline or yaml.schemas) for completion and validation; " +
			"see README \"Editor support\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := cmd.OutOrStdout().Write(schema.TokenSpec); err != nil {
				return fmt.Errorf("write schema: %w", err)
			}
			return nil
		},
	}
}
