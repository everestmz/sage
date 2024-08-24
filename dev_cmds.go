package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

func init() {
	queryFlags := RunQueryCmd.PersistentFlags()
	queryFlags.String("query", "", "The value to query the DB for")

	embeddingFlags := GetEmbeddingCmd.PersistentFlags()
	embeddingFlags.String("text", "", "The text to embed")
	embeddingFlags.String("model", "", "The embedding model to use")

	DevCmds.AddCommand(RunQueryCmd, GetEmbeddingCmd, InspectCmd)
}

var DevCmds = &cobra.Command{
	Use: "dev",
}

var InspectCmd = &cobra.Command{
	Use:   "inspect [cmd args...]",
	Short: "Log all LSP interactions while acting as a passthrough",
	RunE: func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}

		inLog := filepath.Join(wd, "lsp-stdin.log")
		outLog := filepath.Join(wd, "lsp-stdout.log")

		// Open log files
		inFile, err := os.Create(inLog)
		if err != nil {
			return err
		}
		defer inFile.Close()

		outFile, err := os.Create(outLog)
		if err != nil {
			return err
		}
		defer outFile.Close()

		// Start the language server
		cmdSpl := strings.Split(args[0], " ")
		lspCmd := exec.Command(cmdSpl[0], cmdSpl[1:]...)
		serverIn, err := lspCmd.StdinPipe()
		if err != nil {
			return err
		}
		serverOut, err := lspCmd.StdoutPipe()
		if err != nil {
			return err
		}
		serverErr, err := lspCmd.StderrPipe()
		if err != nil {
			return err
		}

		if err := lspCmd.Start(); err != nil {
			return err
		}

		var wg sync.WaitGroup
		wg.Add(3)

		// Log and forward stdin
		go func() {
			defer wg.Done()
			multiWriter := io.MultiWriter(serverIn, inFile)
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Split(bufio.ScanLines)
			for scanner.Scan() {
				line := scanner.Text() + "\n"
				multiWriter.Write([]byte(line))
			}
			if err := scanner.Err(); err != nil {
				println("Error reading from stdin:", err.Error())
			}
			serverIn.Close()
		}()

		// Log and forward stdout
		go func() {
			defer wg.Done()
			multiWriter := io.MultiWriter(os.Stdout, outFile)
			scanner := bufio.NewScanner(serverOut)
			scanner.Split(bufio.ScanLines)
			for scanner.Scan() {
				line := scanner.Text() + "\n"
				multiWriter.Write([]byte(line))
			}
			if err := scanner.Err(); err != nil {
				println("Error reading from stdout:", err.Error())
			}
		}()

		// Log stderr (if any)
		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(serverErr)
			scanner.Split(bufio.ScanLines)
			for scanner.Scan() {
				line := scanner.Text() + "\n"
				outFile.Write([]byte(line))
			}
			if err := scanner.Err(); err != nil {
				println("Error reading from stderr:", err.Error())
			}
		}()

		// Wait for the language server to finish
		if err := lspCmd.Wait(); err != nil {
			return err
		}

		wg.Wait()
		return nil
	},
}

var GetEmbeddingCmd = &cobra.Command{
	Use: "embedding",
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()

		model, err := flags.GetString("model")
		if err != nil {
			return err
		}

		text, err := flags.GetString("text")
		if err != nil {
			return err
		}

		llm, err := NewLLMClient()
		if err != nil {
			return err
		}

		embedding, err := llm.GetEmbedding(context.TODO(), model, text)
		if err != nil {
			return err
		}

		var vecStrs []string
		for _, f := range embedding {
			vecStrs = append(vecStrs, fmt.Sprint(float32(f)))
		}

		fmt.Println("[" + strings.Join(vecStrs, ", ") + "]")
		fmt.Fprintln(os.Stderr, len(vecStrs), "dimensions")
		return nil
	},
}

var RunQueryCmd = &cobra.Command{
	Use: "query-symbols",
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()

		query, err := flags.GetString("query")
		if err != nil {
			return err
		}

		wd, err := os.Getwd()
		if err != nil {
			return err
		}

		db, err := openDB(wd)
		if err != nil {
			return err
		}

		llm, err := NewLLMClient()
		if err != nil {
			return err
		}

		symbols, err := findSymbol(context.TODO(), db, llm, query)
		if err != nil {
			return err
		}

		for _, sym := range symbols {
			fmt.Println(sym.Name, sym.Location)
		}

		return nil
	},
}
