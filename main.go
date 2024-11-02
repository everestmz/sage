package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"github.com/mattn/go-isatty"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
    "github.com/rs/zerolog"
    "github.com/rs/zerolog/log"
)

type SymbolInfo struct {
	protocol.SymbolInformation
	RelativePath string
	Description  string
	Embedding    []float64
}

func main() {
	rootCmd := &cobra.Command{
		Use: "sage",
	}

	rootCmd.AddCommand(IndexCmd, LanguageServerCmd, CompletionCmd, DevCmds, CtxCmd)

	if isatty.IsTerminal(os.Stdout.Fd()) {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func startLsp(lsp *LanguageServerConfig, clientConn *protocol.Client, params *protocol.InitializeParams) (*ChildLanguageServer, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(*lsp.Command, lsp.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	workspaceDir := getWorkspaceDir(wd)
	stdoutFileName := filepath.Join(workspaceDir, "lsp_stdout.log")
	stdoutFile, err := os.Create(stdoutFileName)
	if err != nil {
		return nil, err
	}

	stdinFileName := filepath.Join(workspaceDir, "lsp_stdin.log")
	stdinFile, err := os.Create(stdinFileName)
	if err != nil {
		return nil, err
	}

	stderrFileName := filepath.Join(workspaceDir, "lsp_stderr.log")
	stderrFile, err := os.Create(stderrFileName)
	if err != nil {
		return nil, err
	}

	go func() {
		stderrScanner := bufio.NewScanner(stderr)
		for stderrScanner.Scan() {
			_, err := stderrFile.Write(stderrScanner.Bytes())
			if err != nil {
				panic(err)
			}
			stderrFile.Sync()
		}

		err := stderrFile.Close()
		if err != nil {
			panic(err)
		}
	}()

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	jsonRpcLogFile := filepath.Join(workspaceDir, "json_rpc.log")
	jsonRpcLog, err := os.Create(jsonRpcLogFile)
	if err != nil {
		return nil, err
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig), // Encoder configuration
		zapcore.AddSync(jsonRpcLog),           // Log file
		zap.DebugLevel,                        // Log level
	)

	// Create the logger
	logger := zap.New(core)

	rwc := &CombinedReadWriteCloser{
		Reader: stdout,
		Writer: stdin,
		Closer: func() error {
			err2 := stdin.Close()
			err1 := stdout.Close()
			if err1 != nil || err2 != nil {
				return fmt.Errorf("Stdout:(%w) Stdin:(%w)", err1, err2)
			}

			return cmd.Process.Kill()
		},
		readFile:  stdoutFile,
		writeFile: stdinFile,
	}
	stream := jsonrpc2.NewStream(rwc)
	conn := jsonrpc2.NewConn(stream)

	if clientConn == nil {
		lspClient := protocol.ClientDispatcher(conn, logger)
		clientConn = &lspClient
	}

	ctx, conn, server := protocol.NewClient(context.Background(), *clientConn, stream, zap.L())

	result, err := server.Initialize(ctx, params)
	if err != nil {
		return nil, err
	}

	childLs := &ChildLanguageServer{
		Conn:    conn,
		Cmd:     cmd,
		Server:  server,
		Context: ctx,
		Close: func() {
			logger.Sync()
			jsonRpcLog.Close()
			stdoutFile.Close()
			stdinFile.Close()
			stderrFile.Close()
			conn.Close()
		},
		InitResult: result,
	}

	go func() {
		cmd.Wait()
	}()

	return childLs, nil
}
