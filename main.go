// Command framehood is the Framehood CLI: generate images, video and audio
// from the terminal, either one-shot via subcommands or interactively via the
// studio (run with no arguments).
package main

import (
	"fmt"
	"os"

	"github.com/Framehood/framehood-cli/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
