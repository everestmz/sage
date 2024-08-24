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
	Aliases: []string{"c"},
	RunE: func(cmd *cobra.Command, args []string) error {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		stdinLines := strings.Split(string(input), "\n")

		fmt.Println("Your input was:")
		for _, line := range stdinLines {
			time.Sleep(time.Second)
			fmt.Println(line)
		}
		fmt.Println("Done")

		return nil
	},
}
