package main

import (
	"fmt"
	"os"

	"github.com/everestmz/sage/replace"
	"github.com/spf13/cobra"
)

func init() {
	flags := CtxCmd.PersistentFlags()
	flags.BoolP("print", "p", false, "Always just print the filepath even if an editor is present")
}

var CtxCmd = &cobra.Command{
	Use:   "ctx",
	Short: "Opens the context file for the current directory in $EDITOR if it exists, otherwise prints its filepath",
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()

		editor := os.Getenv("EDITOR")

		path := getWorkspaceContextPath()

		print, err := flags.GetBool("print")
		if err != nil {
			return err
		}

		if editor == "" || print {
			fmt.Println(path)
		}

		replace.Exec(editor, path)
		return nil
	},
}
