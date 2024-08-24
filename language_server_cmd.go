package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.uber.org/zap"
	"gopkg.in/natefinch/lumberjack.v2"
)

type LanguageServerConfig struct {
	Command string         `json:"command"`
	Args    []string       `json:"args"`
	Config  map[string]any `json:"config"`
}

type ChildLanguageServer struct {
	protocol.Server
	Close        func()
	Context      context.Context
	Capabilities protocol.ServerCapabilities
}

type SageLanguageServerConfig struct {
	// This should be in order: i.e. first LS in the list gets the message first,
	// then the next one, etc etc based on capabilities
	// Right now, we'll just support 1 lsp
	LanguageServers []*LanguageServerConfig `json:"language_servers"`
}

var lsLogger = zerolog.New(&lumberjack.Logger{
	Filename:   filepath.Join(getConfigDir(), "sage.log"),
	MaxSize:    50,
	MaxBackups: 10,
}).With().Timestamp().Logger()

func init() {
	flags := LanguageServerCmd.PersistentFlags()

	flags.String("cmd", "", "Specify the command (and arguments) to run, as a single space-separated string")
}

var LanguageServerCmd = &cobra.Command{
	Use: "ls",
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()

		lspCommand, err := flags.GetString("cmd")
		if err != nil {
			return err
		}

		rwc := &CombinedReadWriteCloser{
			Reader: os.Stdin,
			Writer: os.Stdout,
			Closer: func() error {
				return nil
			},
		}

		// I don't know if we actually need this, but this is a way for
		// us to close the connection from within the dispatcher/handler
		closeChan := make(chan bool)

		ctx := context.TODO()
		stream := jsonrpc2.NewStream(rwc)
		conn := jsonrpc2.NewConn(stream)
		client := protocol.ClientDispatcher(conn, zap.L())
		ctx = protocol.WithClient(ctx, client)

		lspCommandSplit := strings.Split(lspCommand, " ")

		// Taking a page out of protocol.NewServer
		dispatcher, err := GetLanguageServerDispatcher(closeChan, client, lspCommandSplit)
		if err != nil {
			return err
		}

		conn.Go(
			ctx, protocol.Handlers(
				dispatcher,
			),
		)

		select {
		case <-closeChan:
			err := conn.Close()
			if err != nil {
				return err
			}
		case <-conn.Done():
			close(closeChan)
		}

		return nil
	},
}

type LanguageServerClientInfo struct {
	OpenDocuments map[string]protocol.TextDocumentItem
}

func GetLanguageServerDispatcher(closeChan chan bool, clientConn protocol.Client, lsCommand []string) (func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error, error) {
	clientInfo := &LanguageServerClientInfo{
		OpenDocuments: map[string]protocol.TextDocumentItem{},
	}
	var ls *ChildLanguageServer

	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	db, err := openDB(wd)
	if err != nil {
		return nil, err
	}

	handler := func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		lsLogger.Info().Str("method", req.Method()).RawJSON("params", req.Params()).Msg("Received message from client")

		switch req.Method() {
		case protocol.MethodInitialize:
			params := &protocol.InitializeParams{}
			err = json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			ls, err = startLsp(&LanguageServerConfig{
				Command: lsCommand[0],
				Args:    lsCommand[1:],
			}, wd, &clientConn, params.Capabilities)
			if err != nil {
				return err
			}

			// InitializedParams has no fields
			result := map[string]any{
				"capabilities": ls.Capabilities,
			}

			return reply(ctx, result, nil)
		case protocol.MethodTextDocumentDidOpen:
			params := &protocol.DidOpenTextDocumentParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			clientInfo.OpenDocuments[params.TextDocument.URI.Filename()] = params.TextDocument

		case protocol.MethodTextDocumentDidClose:
			params := &protocol.DidCloseTextDocumentParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			delete(clientInfo.OpenDocuments, params.TextDocument.URI.Filename())
		case protocol.MethodWorkspaceSymbol:
			params := &protocol.WorkspaceSymbolParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			symbols, err := db.FindSymbolByPrefix(params.Query)
			if err != nil {
				return err
			}

			return reply(ctx, symbols, err)
		case protocol.MethodExit:
			fallthrough
		case protocol.MethodShutdown:
			// We kill the servers:

			// And then kill the connection to the parent
			close(closeChan)
			return nil
		}

		// We pass through to the language server
		lsLogger.Info().Str("method", req.Method()).Msg("Passing through to child")
		result, err := ls.Request(ls.Context, req.Method(), req.Params())
		return reply(ctx, result, err)
	}

	return func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		err := handler(ctx, reply, req)
		if err != nil {
			lsLogger.Error().Str("method", req.Method()).Err(err).Msg("Error handling request")
		}
		return err
	}, nil
}
