// SPDX-License-Identifier: AGPL-3.0-only

// Команда keycrm-mcp — MCP-сервер для keyCRM.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// version заповнюється під час релізу через -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "keycrm-mcp:", err)
		os.Exit(1)
	}
}

// run розбирає аргументи командного рядка й виконує команду.
func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("keycrm-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	showVersion := fs.Bool("version", false, "показати версію і вийти")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(stdout)
			fs.PrintDefaults()
			return nil
		}
		return fmt.Errorf("розбір аргументів: %w", err)
	}
	if *showVersion {
		if _, err := fmt.Fprintf(stdout, "keycrm-mcp %s\n", version); err != nil {
			return fmt.Errorf("виведення версії: %w", err)
		}
		return nil
	}
	return errors.New("сервер ще не реалізовано; доступний лише --version")
}
