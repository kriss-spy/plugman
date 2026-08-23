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

// Info writes a human-readable release summary.
func Info(writer io.Writer, result model.Report) error {
	if result.Info == nil {
		return fmt.Errorf("report contains no plugin info")
	}
	value := result.Info
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	rows := [][2]string{
		{"ID", value.ID},
		{"Name", value.Name},
		{"Installed", string(value.Installation)},
		{"Newest compatible", value.NewestCompatibleVersion},
		{"Requires Obsidian", value.MinimumObsidianVersion},
		{"Desktop only", fmt.Sprintf("%t", value.DesktopOnly)},
		{"Source", string(value.Source.Kind)},
		{"Release", value.ReleaseURL},
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(table, "%s:\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	return table.Flush()
}

// Plan writes a deterministic mutation plan.
func Plan(writer io.Writer, result model.Report) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "PLUGIN\tCURRENT\tTARGET\tACTION"); err != nil {
		return err
	}
	for _, entry := range result.Plan {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", entry.ID, optionalString(entry.CurrentVersion, "-"), entry.TargetVersion, entry.Action); err != nil {
			return err
		}
	}
	return table.Flush()
}

// Results writes ordered application and restoration outcomes.
func Results(writer io.Writer, result model.Report) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "PLUGIN\tACTION\tRESULT"); err != nil {
		return err
	}
	for _, item := range result.Results {
		outcome := "unchanged"
		switch {
		case item.Error != "" && item.Restored:
			outcome = "failed; restored"
		case item.Error != "":
			outcome = "failed"
		case item.Changed:
			outcome = "changed"
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\n", item.ID, item.Action, outcome); err != nil {
			return err
		}
	}
	return table.Flush()
}

// Outdated writes stable installed-versus-latest state.
func Outdated(writer io.Writer, result model.Report) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "PLUGIN\tCURRENT\tLATEST\tENABLED\tSOURCE\tSTATE\tRELEASE"); err != nil {
		return err
	}
	for _, item := range result.Outdated {
		sourceValue := string(item.Source.Kind)
		if item.Source.Repository != nil {
			sourceValue = *item.Source.Repository
		}
		latest := item.LatestVersion
		if latest == "" {
			latest = "-"
		}
		release := item.ReleaseURL
		if release == "" {
			release = "-"
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%t\t%s\t%s\t%s\n", item.ID, item.CurrentVersion, latest, item.Enabled, sourceValue, item.State, release); err != nil {
			return err
		}
	}
	return table.Flush()
}

// UninstallSummary writes the facts shown before one batch confirmation.
func UninstallSummary(writer io.Writer, result model.Report) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "PLUGIN\tVERSION\tENABLED\tDATA"); err != nil {
		return err
	}
	for _, item := range result.Uninstall {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%t\t%t\n", item.ID, item.Version, item.Enabled, item.HasData); err != nil {
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
