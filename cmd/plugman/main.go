package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kriss-spy/plugman/internal/manager"
	"github.com/kriss-spy/plugman/internal/model"
	"github.com/kriss-spy/plugman/internal/report"
)

var version = "dev"

func main() {
	vaultRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	interactive := false
	if info, statErr := os.Stdin.Stat(); statErr == nil {
		interactive = info.Mode()&os.ModeCharDevice != 0
	}
	os.Exit(runCommand(os.Args[1:], vaultRoot, os.Stdin, os.Stdout, os.Stderr, interactive))
}

func run(args []string, vaultRoot string, stdout, stderr io.Writer) int {
	return runCommand(args, vaultRoot, strings.NewReader(""), stdout, stderr, false)
}

func runCommand(args []string, vaultRoot string, stdin io.Reader, stdout, stderr io.Writer, interactive bool) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printHelp(stdout)
		return 0
	}
	if args[0] == "--version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if args[0] == "info" {
		return runInfo(args[1:], vaultRoot, stdout, stderr, interactive)
	}
	if args[0] == "install" {
		return runInstall(args[1:], vaultRoot, stdout, stderr)
	}
	if args[0] == "update" {
		return runUpdate(args[1:], vaultRoot, stdout, stderr)
	}
	if args[0] == "outdated" {
		return runOutdated(args[1:], vaultRoot, stdout, stderr, interactive)
	}
	if args[0] == "uninstall" {
		return runUninstall(args[1:], vaultRoot, stdin, stdout, stderr, interactive)
	}
	if args[0] == "export" {
		return runExport(args[1:], vaultRoot, stdout, stderr)
	}
	if args[0] != "list" {
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}

	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	enabledOnly := flags.Bool("enabled", false, "list enabled plugins only")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if exitCode, done := parseFlags(flags, args[1:]); done {
		return exitCode
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "plugman list does not accept arguments")
		return 2
	}

	result, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{
		Kind: model.OperationList,
		List: model.ListOptions{EnabledOnly: *enabledOnly},
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		err = report.JSON(stdout, result)
	} else {
		err = report.Table(stdout, result)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runInstall(args []string, vaultRoot string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	enable := flags.Bool("enable", false, "enable newly installed plugins")
	allowDowngrade := flags.Bool("allow-downgrade", false, "allow exact versions older than installed")
	dryRun := flags.Bool("dry-run", false, "resolve and print the plan without changing the Vault")
	if exitCode, done := parseFlags(flags, args); done {
		return exitCode
	}
	if flags.NArg() == 0 {
		fmt.Fprintln(stderr, "plugman install requires at least one plugin ID, Plugin List path, or GitHub URL")
		return 2
	}
	result, err := manager.NewWithConfig(vaultRoot, manager.Config{PlanReady: func(result model.Report) error {
		return report.Plan(stdout, result)
	}}).Run(context.Background(), model.Operation{
		Kind:    model.OperationInstall,
		Install: model.InstallOptions{Inputs: flags.Args(), Enable: *enable, AllowDowngrade: *allowDowngrade, DryRun: *dryRun},
	})
	if err != nil {
		if len(result.Results) != 0 {
			_ = report.Results(stdout, result)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(result.Results) != 0 {
		err = report.Results(stdout, result)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runUpdate(args []string, vaultRoot string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	allowDowngrade := flags.Bool("allow-downgrade", false, "allow versions older than installed")
	dryRun := flags.Bool("dry-run", false, "resolve and print the plan without changing the Vault")
	if exitCode, done := parseFlags(flags, args); done {
		return exitCode
	}
	result, err := manager.NewWithConfig(vaultRoot, manager.Config{PlanReady: func(result model.Report) error {
		return report.Plan(stdout, result)
	}}).Run(context.Background(), model.Operation{
		Kind:   model.OperationUpdate,
		Update: model.UpdateOptions{Inputs: flags.Args(), AllowDowngrade: *allowDowngrade, DryRun: *dryRun},
	})
	if err != nil {
		if len(result.Results) != 0 {
			_ = report.Results(stdout, result)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(result.Results) != 0 {
		if err := report.Results(stdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

func runOutdated(args []string, vaultRoot string, stdout, stderr io.Writer, interactive bool) int {
	flags := flag.NewFlagSet("outdated", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if exitCode, done := parseFlags(flags, args); done {
		return exitCode
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "plugman outdated does not accept arguments")
		return 2
	}
	if interactive {
		fmt.Fprintln(stderr, "Checking plugin releases...")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := manager.New(vaultRoot).Run(ctx, model.Operation{Kind: model.OperationOutdated})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		err = report.JSON(stdout, result)
	} else {
		err = report.Outdated(stdout, result)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runUninstall(args []string, vaultRoot string, stdin io.Reader, stdout, stderr io.Writer, interactive bool) int {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	keepData := flags.Bool("keep-data", false, "preserve plugin data.json")
	yes := flags.Bool("yes", false, "skip interactive confirmation")
	dryRun := flags.Bool("dry-run", false, "print the plan without changing the Vault")
	if exitCode, done := parseFlags(flags, args); done {
		return exitCode
	}
	if flags.NArg() == 0 {
		fmt.Fprintln(stderr, "plugman uninstall requires at least one plugin ID")
		return 2
	}
	operation := model.Operation{Kind: model.OperationUninstall, Uninstall: model.UninstallOptions{
		IDs: flags.Args(), KeepData: *keepData, Yes: *yes, DryRun: *dryRun,
	}}
	configured := manager.NewWithConfig(vaultRoot, manager.Config{PlanReady: func(result model.Report) error { return report.Plan(stdout, result) }})
	result, err := configured.Run(context.Background(), operation)
	var confirmation *model.ConfirmationRequiredError
	if errors.As(err, &confirmation) {
		if !interactive {
			fmt.Fprintln(stderr, "non-interactive uninstall requires --yes")
			return 1
		}
		if renderErr := report.UninstallSummary(stdout, result); renderErr != nil {
			fmt.Fprintln(stderr, renderErr)
			return 1
		}
		fmt.Fprint(stdout, "Uninstall these plugins? [y/N] ")
		line, readErr := bufio.NewReader(stdin).ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			fmt.Fprintln(stderr, readErr)
			return 1
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(stdout, "Cancelled.")
			return 0
		}
		operation.Uninstall.Yes = true
		result, err = configured.Run(context.Background(), operation)
	}
	if err != nil {
		if len(result.Results) != 0 {
			_ = report.Results(stdout, result)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(result.Results) != 0 {
		if err := report.Results(stdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

func runExport(args []string, vaultRoot string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	enabled := flags.Bool("enabled", false, "export enabled plugins only")
	latest := flags.Bool("latest", false, "omit exact release versions")
	force := flags.Bool("force", false, "overwrite an existing destination")
	if exitCode, done := parseFlags(flags, args); done {
		return exitCode
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "plugman export requires one Plugin List path")
		return 2
	}
	result, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationExport, Export: model.ExportOptions{
		Path: flags.Arg(0), EnabledOnly: *enabled, Latest: *latest, Force: *force,
	}})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Exported %d plugins to %s\n", result.Export.Written, result.Export.Path)
	return 0
}

func runInfo(args []string, vaultRoot string, stdout, stderr io.Writer, interactive bool) int {
	flags := flag.NewFlagSet("info", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if exitCode, done := parseFlags(flags, args); done {
		return exitCode
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "plugman info requires one plugin ID or GitHub URL")
		return 2
	}
	if interactive {
		fmt.Fprintln(stderr, "Checking plugin metadata...")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := manager.New(vaultRoot).Run(ctx, model.Operation{
		Kind: model.OperationInfo,
		Info: model.InfoOptions{Input: flags.Arg(0)},
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		err = report.JSON(stdout, result)
	} else {
		err = report.Info(stdout, result)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func printHelp(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: plugman <command> [options]")
	fmt.Fprintln(writer, "Commands: install, update, uninstall, list, outdated, info, export")
}

func parseFlags(flags *flag.FlagSet, args []string) (int, bool) {
	err := flags.Parse(args)
	if err == nil {
		return 0, false
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0, true
	}
	return 2, true
}
