package report

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/kriss-spy/plugman/internal/model"
)

// JSON writes the stable machine-readable report schema.
func JSON(writer io.Writer, result model.Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}

// Table writes a human-readable list without terminal-specific styling.
func Table(writer io.Writer, result model.Report) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "ID\tVERSION\tENABLED\tSOURCE\tSTATUS\tPROBLEM"); err != nil {
		return err
	}
	for _, plugin := range result.Plugins {
		id := optionalString(plugin.ID, plugin.Folder)
		version := optionalString(plugin.Version, "-")
		enabled := "unknown"
		if plugin.Enabled != nil {
			enabled = fmt.Sprintf("%t", *plugin.Enabled)
		}
		source := string(plugin.Source.Kind)
		if plugin.Source.Repository != nil {
			source = *plugin.Source.Repository
		}
		problem := "-"
		if len(plugin.Problems) > 0 {
			problem = plugin.Problems[0].Message
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", id, version, enabled, source, plugin.Status, problem); err != nil {
			return err
		}
	}
	return table.Flush()
}

func optionalString(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}
