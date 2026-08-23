package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

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
	os.Exit(run(os.Args[1:], vaultRoot, os.Stdout, os.Stderr))
}

func run(args []string, vaultRoot string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}
	if args[0] == "--version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if args[0] != "list" {
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}

	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	enabledOnly := flags.Bool("enabled", false, "list enabled plugins only")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
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

func printHelp(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: plugman list [--enabled] [--json]")
}
