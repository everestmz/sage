package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var CompletionCmd = &cobra.Command{
	Use:     "complete",
	Short:   "Run a question within the context of the working directory",
	Aliases: []string{"c", "ask"},
	RunE: func(cmd *cobra.Command, args []string) error {
		var stdinLines []string
		if len(args) > 0 {
			stdinLines = args
		} else {
			input, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}

			stdinLines = strings.Split(string(input), "\n")
		}

		fmt.Println("Your input was:")
		for _, line := range stdinLines {
			time.Sleep(time.Second)
			fmt.Println(line)
		}

		return nil
	},
}
