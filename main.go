package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/zap"
)

var PyrightLangserver = &LanguageServerConfig{
	Command: "pyright-langserver",
	Args:    []string{"--stdio"},
	Config:  map[string]any{},
}

type SymbolInfo struct {
	protocol.SymbolInformation
	RelativePath string
}

func main() {
	rootCmd := &cobra.Command{
		Use: "sage",
	}

	rootCmd.AddCommand(IndexCmd, LanguageServerCmd)

	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func startLsp(lsp *LanguageServerConfig, folder string, clientConn *protocol.Client, capabilities protocol.ClientCapabilities) (*ChildLanguageServer, error) {
	cmd := exec.Command(lsp.Command, lsp.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

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
	}

	fmt.Println("Stream init...")
	stream := jsonrpc2.NewStream(rwc)
	conn := jsonrpc2.NewConn(stream)

	if clientConn == nil {
		lspClient := protocol.ClientDispatcher(conn, zap.L())
		clientConn = &lspClient
	}

	ctx, conn, server := protocol.NewClient(context.Background(), *clientConn, stream, zap.L())

	result, err := server.Initialize(ctx, &protocol.InitializeParams{
		ProcessID:    int32(os.Getpid()),
		Capabilities: capabilities,
		WorkspaceFolders: []protocol.WorkspaceFolder{
			{
				URI:  string(uri.File(folder)),
				Name: "Main workspace",
			},
		},
	})
	if err != nil {
		return nil, err
	}

	fmt.Println("Setting config...")
	err = server.DidChangeConfiguration(ctx, &protocol.DidChangeConfigurationParams{
		Settings: map[string]any{},
	})
	if err != nil {
		return nil, err
	}
	fmt.Println("Config set!")

	return &ChildLanguageServer{
		Server:  server,
		Context: ctx,
		Close: func() {
			conn.Close()
		},
		Capabilities: result.Capabilities,
	}, nil
}
