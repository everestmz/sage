package main

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/everestmz/sage/locality"
	"github.com/everestmz/sage/lsp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/zap"
	"gopkg.in/natefinch/lumberjack.v2"
)

type LanguageServerConfig struct {
	Command *string        `json:"command" yaml:"command"`
	Args    []string       `json:"args" yaml:"args"`
	Config  map[string]any `json:"config" yaml:"config"`
}

type ChildLanguageServer struct {
	protocol.Server
	Conn       jsonrpc2.Conn
	Cmd        *exec.Cmd
	Close      func()
	Context    context.Context
	InitResult *protocol.InitializeResult
}

type SageLanguageServerConfig struct {
	// This should be in order: i.e. first LS in the list gets the message first,
	// then the next one, etc etc based on capabilities
	// Right now, we'll just support 1 lsp
	LanguageServers []*LanguageServerConfig `json:"language_servers"`
}

var globalLsLogger = zerolog.New(&lumberjack.Logger{
	Filename:   filepath.Join(getConfigDir(), "sage.log"),
	MaxSize:    50,
	MaxBackups: 10,
}).Level(zerolog.DebugLevel).With().Timestamp().Logger()

func init() {
	flags := LanguageServerCmd.PersistentFlags()

	flags.String("cmd", "", "Specify the command (and arguments) to run, as a single space-separated string")
}

var _ jsonrpc2.Conn = &LspConnLogger{}

type LspConnLogger struct {
	conn jsonrpc2.Conn
	log  zerolog.Logger
}

// Call implements jsonrpc2.Conn.
func (l *LspConnLogger) Call(ctx context.Context, method string, params interface{}, result interface{}) (jsonrpc2.ID, error) {
	paramsBs, err := json.Marshal(params)
	if err != nil {
		return jsonrpc2.ID{}, err
	}

	logMsg := l.log.Info().
		Str("method", method).
		RawJSON("params", paramsBs)

	msgId, err := l.conn.Call(ctx, method, params, result)
	if err != nil {
		logMsg = logMsg.AnErr("err", err)
	} else {
		resultBs, err := json.Marshal(params)
		if err != nil {
			return jsonrpc2.ID{}, err
		}

		logMsg = logMsg.RawJSON("result", resultBs)
	}

	logMsg.Msg("Call")

	return msgId, err
}

// Close implements jsonrpc2.Conn.
func (l *LspConnLogger) Close() error {
	return l.conn.Close()
}

// Done implements jsonrpc2.Conn.
func (l *LspConnLogger) Done() <-chan struct{} {
	return l.conn.Done()
}

// Err implements jsonrpc2.Conn.
func (l *LspConnLogger) Err() error {
	return l.conn.Err()
}

// Go implements jsonrpc2.Conn.
func (l *LspConnLogger) Go(ctx context.Context, handler jsonrpc2.Handler) {
	l.conn.Go(ctx, handler)
}

// Notify implements jsonrpc2.Conn.
func (l *LspConnLogger) Notify(ctx context.Context, method string, params interface{}) error {
	paramsBs, err := json.Marshal(params)
	if err != nil {
		return err
	}

	logMsg := l.log.Info().
		Str("method", method).
		RawJSON("params", paramsBs)

	err = l.conn.Notify(ctx, method, params)
	if err != nil {
		logMsg = logMsg.AnErr("err", err)
	}

	logMsg.Msg("Notify")
	return err
}

type LspClient struct {
	protocol.Client

	conn jsonrpc2.Conn
}

func (c *LspClient) Conn() jsonrpc2.Conn {
	return c.conn
}

var LanguageServerCmd = &cobra.Command{
	Use:   "ls",
	Short: "Start the sage language server",
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
		client := LspClient{
			Client: protocol.ClientDispatcher(conn, zap.L()),
			conn:   conn,
		}
		ctx = protocol.WithClient(ctx, client)

		lspCommandSplit := strings.Split(lspCommand, " ")

		config, err := getConfigForWd()
		if err != nil {
			return err
		}

		// Taking a page out of protocol.NewServer
		dispatcher, err := GetLanguageServerDispatcher(closeChan, client, lspCommandSplit, config)
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

func NewLanguageServerClientInfo(config *SagePathConfig, llm *LLMClient) *LanguageServerClientInfo {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	db, err := openDB(wd)
	if err != nil {
		panic(err)
	}

	return &LanguageServerClientInfo{
		LLM:    llm,
		Config: config,

		openDocuments: map[uri.URI]*protocol.TextDocumentItem{},
		docLock:       &sync.Mutex{},
		stateDir:      filepath.Join(getConfigDir(), "state"),
		db:            db,
		wd:            wd,
	}
}

type LanguageServerClientInfo struct {
	LLM    *LLMClient
	Config *SagePathConfig

	openDocuments map[uri.URI]*protocol.TextDocumentItem
	docLock       *sync.Mutex
	stateDir      string
	db            *DB
	wd            string
}

func (ci *LanguageServerClientInfo) GetSymbol(filename string, symbol string) (string, error) {
	symbols, err := ci.db.FindSymbolByPrefix(symbol)
	if err != nil {
		return "", err
	}

	// XXX: there be dragons here! If a file is modified but the new modifications haven't been indexed,
	// then we're in for trouble, since the ranges will be wrong! Maybe ok if we cache at a higher level.
	// Maybe we need to build in some reindexing into our LSP - either that, or we need to cache the symbol
	// text in the DB, but that'd probably grow the size too much.

	for _, sym := range symbols {
		if strings.HasSuffix(sym.Location.URI.Filename(), filename) {
			fileContent, err := ci.GetFile(filename)
			if err != nil {
				return "", err
			}
			symbolText := lsp.GetRangeFromFile(fileContent, sym.Location.Range)
			return symbolText, nil
		}
	}

	return "", fmt.Errorf("Symbol '%s' not found for filename '%s' - check naming", symbol, filename)
}

func (ci *LanguageServerClientInfo) GetFile(filename string) (string, error) {
	openDoc := ci.GetOpenDocument(uri.File(filename))
	if openDoc != nil {
		return openDoc.Text, nil
	}

	fileBytes, err := os.ReadFile(filepath.Join(ci.wd, filename))
	return string(fileBytes), err
}

func (ci *LanguageServerClientInfo) GetRange(filename string, start, end int) (string, error) {
	fileContent, err := ci.GetFile(filename)
	if err != nil {
		return "", err
	}

	// Convert to indices
	start = start - 1
	if start < 0 {
		start = 0
	}

	lines := strings.Split(fileContent, "\n")

	if start >= len(lines) {
		return "", fmt.Errorf("Range start '%d' > length of file (%d lines)", start, len(lines))
	}

	if end >= len(lines) {
		end = len(lines) - 1
	}

	return strings.Join(lines[start:end], "\n"), nil
}

func (ci *LanguageServerClientInfo) updateState(uri uri.URI) {
	path := filepath.Join(ci.stateDir, uri.Filename())
	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		panic(err)
	}
	err = os.WriteFile(
		path,
		[]byte(ci.openDocuments[uri].Text),
		0755,
	)
	if err != nil {
		panic(err)
	}
}

func (ci *LanguageServerClientInfo) clearState(uri uri.URI) {
	err := os.RemoveAll(filepath.Join(ci.stateDir, uri.Filename()))
	if err != nil {
		panic(err)
	}
}

func (ci *LanguageServerClientInfo) GetOpenDocument(uri uri.URI) *protocol.TextDocumentItem {
	ci.docLock.Lock()
	defer ci.docLock.Unlock()

	return ci.openDocuments[uri]
}

func (ci *LanguageServerClientInfo) OpenDocument(doc *protocol.TextDocumentItem) {
	ci.docLock.Lock()
	defer ci.docLock.Unlock()

	ci.openDocuments[doc.URI] = doc
	ci.updateState(doc.URI)
}

func (ci *LanguageServerClientInfo) CloseDocument(uri uri.URI) {
	ci.docLock.Lock()
	defer ci.docLock.Unlock()

	ci.clearState(uri)
	delete(ci.openDocuments, uri)
}

func (ci *LanguageServerClientInfo) EditDocument(uri uri.URI, editFunc func(doc *protocol.TextDocumentItem) error) error {
	ci.docLock.Lock()
	defer ci.docLock.Unlock()

	err := editFunc(ci.openDocuments[uri])
	if err != nil {
		return err
	}

	ci.updateState(uri)
	return nil
}

type CommandDefinition struct {
	Title          string
	Identifier     string
	ShowCodeAction bool
	BuildArgs      func(params *protocol.CodeActionParams) (args []any, err error)
	Execute        func(params *protocol.ExecuteCommandParams, client LspClient, clientInfo *LanguageServerClientInfo) (*protocol.ApplyWorkspaceEditParams, error)
}

func (cd *CommandDefinition) BuildDefinition(params *protocol.CodeActionParams) (*protocol.Command, error) {
	args, err := cd.BuildArgs(params)
	if err != nil {
		return nil, err
	}

	return &protocol.Command{
		Title:     cd.Title,
		Command:   cd.Identifier,
		Arguments: args,
	}, nil
}

type LlmCompletionArgs struct {
	Filename  uri.URI
	Selection protocol.Range
}

func findSymbol(ctx context.Context, db *DB, llm *LLMClient, query string) ([]protocol.SymbolInformation, error) {
	symbols, err := db.FindSymbolByPrefix(query)
	if err != nil {
		return nil, err
	}
	if len(symbols) > 0 {
		return symbols, nil
	}

	if len(query) < 10 || !strings.Contains(query, " ") { // This heuristic is hacky, but it should stop basic issues
		return nil, nil
	}

	// We're getting no symbols from the DB and our query is long enough
	// Time to try a semantic search
	embedding, err := llm.GetEmbedding(ctx, "nomic-embed-text", query)
	if err != nil {
		return nil, err
	}

	return db.FindSymbolByEmbedding(embedding)
}

func isNotification(method string) bool {
	switch method {
	case protocol.MethodInitialized:
		fallthrough
	case protocol.MethodExit:
		fallthrough
	case protocol.MethodWorkDoneProgressCancel:
		fallthrough
	case protocol.MethodLogTrace:
		fallthrough
	case protocol.MethodSetTrace:
		fallthrough
	case protocol.MethodTextDocumentDidChange:
		fallthrough
	case protocol.MethodWorkspaceDidChangeConfiguration:
		fallthrough
	case protocol.MethodWorkspaceDidChangeWatchedFiles:
		fallthrough
	case protocol.MethodWorkspaceDidChangeWorkspaceFolders:
		fallthrough
	case protocol.MethodTextDocumentDidClose:
		fallthrough
	case protocol.MethodTextDocumentDidOpen:
		fallthrough
	case protocol.MethodTextDocumentDidSave:
		fallthrough
	case protocol.MethodTextDocumentWillSave:
		fallthrough
	case protocol.MethodDidCreateFiles:
		fallthrough
	case protocol.MethodDidRenameFiles:
		fallthrough
	case protocol.MethodDidDeleteFiles:
		return true
	default:
		return false
	}
}

func GetLanguageServerDispatcher(closeChan chan bool, clientConn LspClient, lsCommand []string, config *SagePathConfig) (func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error, error) {
	llm, err := NewLLMClient()
	if err != nil {
		return nil, err
	}

	clientInfo := NewLanguageServerClientInfo(config, llm)
	var ls *ChildLanguageServer

	logLock := &sync.Mutex{}

	var l *locality.Locality

	handler := func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		logLock.Lock()
		defer logLock.Unlock()
		hasher := sha1.New()
		hasher.Write(req.Params())
		lsLogger := globalLsLogger.With().Str("method", req.Method()).Str("request_hash", fmt.Sprintf("%x", hasher.Sum(nil))).Int("request_timestamp", int(time.Now().UnixNano())).Logger()
		lsLogger.Info().RawJSON("params", req.Params()).Msg("Received message from client")

		switch req.Method() {
		case protocol.MethodInitialize:
			params := &protocol.InitializeParams{}
			err = json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			params.ProcessID = int32(os.Getpid())
			ls, err = startLsp(&LanguageServerConfig{
				Command: &lsCommand[0],
				Args:    lsCommand[1:],
			}, &clientConn.Client, params)
			if err != nil {
				return err
			}
			l = locality.New(ls)

			capabilitiesJson, err := json.Marshal(ls.InitResult)
			if err != nil {
				return err
			}
			lsLogger.Info().
				RawJSON("lsp_init_result", capabilitiesJson).
				RawJSON("lsp_config", req.Params()).
				Str("command", ls.Cmd.String()).
				Msg("Started LSP")

			// We need to add our own capabilities in here
			if ls.InitResult.Capabilities.ExecuteCommandProvider == nil {
				ls.InitResult.Capabilities.ExecuteCommandProvider = &protocol.ExecuteCommandOptions{}
			}

			for _, cmd := range lspCommands {
				ls.InitResult.Capabilities.ExecuteCommandProvider.Commands = append(ls.InitResult.Capabilities.ExecuteCommandProvider.Commands, cmd.Identifier)
			}

			return reply(ctx, ls.InitResult, nil)

		// case protocol.MethodWorkspaceDidChangeConfiguration:
		// 	params := &protocol.DidChangeConfigurationParams{}
		// 	err := json.Unmarshal(req.Params(), params)
		// 	if err != nil {
		// 		return err
		// 	}

		// 	return reply(ctx, nil, ls.DidChangeConfiguration(ctx, params))

		case protocol.MethodInitialized:
			// XXX: pretty sure we need to send initialized through to the client
			// but we should verify if this breaks pyright since it may
			// return reply(ctx, nil, nil)

		case protocol.MethodTextDocumentDidOpen:
			params := &protocol.DidOpenTextDocumentParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			clientInfo.OpenDocument(&params.TextDocument)

			return reply(ctx, nil, ls.DidOpen(ctx, params))

		case protocol.MethodTextDocumentDidClose:
			params := &protocol.DidCloseTextDocumentParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			clientInfo.CloseDocument(params.TextDocument.URI)

			return reply(ctx, nil, ls.DidClose(ctx, params))

		case protocol.MethodTextDocumentDidChange:
			params := &protocol.DidChangeTextDocumentParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			err = clientInfo.EditDocument(params.TextDocument.URI, func(doc *protocol.TextDocumentItem) error {
				newText, err := applyChangesToDocument(doc.Text, params.ContentChanges)
				if err != nil {
					lsLogger.Error().Err(err).Msg("Error applying edits")
					return err
				}

				doc.Text = newText
				lsLogger.Info().Str("after_edit", newText).Msg("After edits applied")
				return nil
			})
			if err != nil {
				return err
			}

			// No reply from us - pass to child lsp
			return reply(ctx, nil, ls.DidChange(ctx, params))

		case protocol.MethodWorkspaceSymbol:
			params := &protocol.WorkspaceSymbolParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			symbols, err := clientInfo.db.FindSymbolByPrefix(params.Query)
			if err != nil {
				return err
			}

			return reply(ctx, symbols, err)

		case protocol.MethodTextDocumentCodeAction:
			params := &protocol.CodeActionParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			resp := []protocol.CodeAction{}
			for _, cmd := range lspCommands {
				args, err := cmd.BuildArgs(params)
				if err != nil {
					return err
				}

				if cmd.ShowCodeAction {
					resp = append(resp, protocol.CodeAction{
						Title: "Sage: " + cmd.Title,
						// XXX: is this correct? idk
						Kind: protocol.Refactor,
						Command: &protocol.Command{
							Title:     cmd.Title,
							Command:   cmd.Identifier,
							Arguments: args,
						},
					})
				}
			}

			childActions, err := ls.CodeAction(ctx, params)
			if err != nil {
				return err
			}

			return reply(ctx, append(resp, childActions...), nil)
		case protocol.MethodWorkspaceExecuteCommand:
			params := &protocol.ExecuteCommandParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			var cmd *CommandDefinition
			for _, def := range lspCommands {
				if def.Identifier == params.Command {
					cmd = def
					break
				}
			}

			if cmd == nil {
				// We just want to pass through to the child
				break
			}

			reply(ctx, []any{}, err)

			edit, err := cmd.Execute(params, clientConn, clientInfo)
			if err != nil {
				return err
			}

			if edit != nil {
				ok, err := clientConn.ApplyEdit(ctx, edit)
				if err != nil {
					// Error in LSP implementation we should fix
					if err.Error() != "unmarshaling result: json: cannot unmarshal \"{\\\"applied\\\":true}\" into Go value of type bool" {
						return err
					}
				}

				lsLogger.Debug().Bool("apply_edit_result", ok).Msg("Result of applying edit")
			}

			return err

		case protocol.MethodTextDocumentHover:
			params := &protocol.HoverParams{}
			err := json.Unmarshal(req.Params(), params)
			if err != nil {
				return err
			}

			fileName := params.TextDocument.URI.Filename()
			fileContent, err := clientInfo.GetFile(fileName)
			if err != nil {
				return err
			}

			go func() {
				_, err := l.GetContext(fileName, fileContent, int(params.Position.Line))
				if err != nil {
					log.Error().Err(err).Msg("Error getting locality context")
				}
			}()

			// no return, pass through

		case protocol.MethodExit:
			fallthrough
		case protocol.MethodShutdown:
			// We kill the servers: (this will happen in the passthrough)
			ls.Close()

			// And then kill the connection to the parent
			defer close(closeChan)
		}

		// We pass through to the language server

		var result any = nil
		if isNotification(req.Method()) {
			lsLogger.Info().Msg("Passing through to child as notification")
			ls.Conn.Notify(ctx, req.Method(), req.Params())
		} else {
			lsLogger.Info().Msg("Passing through to child as method")
			result, err = ls.Server.Request(ctx, req.Method(), req.Params())
		}
		if err != nil {
			if jrpcErr, ok := err.(*jsonrpc2.Error); ok {
				errLog := lsLogger.Info().
					Int32("code", int32(jrpcErr.Code)).
					Str("message", jrpcErr.Message).
					Err(err)

				if ls.Cmd.ProcessState != nil {
					errLog = errLog.
						Bool("cmd_exited", ls.Cmd.ProcessState.Exited()).
						Bool("cmd_success", ls.Cmd.ProcessState.Success()).
						Int("cmd_exit_code", ls.Cmd.ProcessState.ExitCode())
				}

				if jrpcErr.Data != nil {
					errLog = errLog.RawJSON("data", *jrpcErr.Data)
				}
				errLog.Msg("JsonRPC error response from child")
			} else {
				lsLogger.Info().Err(err).Type("err_type", err).Msg("Error response from child")
			}
			return reply(ctx, nil, err)
		}

		if result != nil {
			resp_bs, err := json.Marshal(result)
			if err != nil {
				lsLogger.Info().Err(err).Msg("Error marshaling response from child")
				return reply(ctx, nil, err)
			}
			lsLogger.Info().Str("resp", string(resp_bs)).Msg("Response from child")
		} else {
			lsLogger.Info().Msg("Empty response from child")
		}
		return reply(ctx, result, err)
	}

	return func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		err := handler(ctx, reply, req)
		if err != nil {
			globalLsLogger.Error().Str("method", req.Method()).Err(err).Msg("Error handling request")
		}
		return err
	}, nil
}

func applyChangesToDocument(textDocument string, changes []protocol.TextDocumentContentChangeEvent) (string, error) {
	lines := strings.Split(textDocument, "\n")

	for _, change := range changes {
		start := change.Range.Start
		end := change.Range.End

		if start.Line >= uint32(len(lines)) || end.Line >= uint32(len(lines)) {
			return "", fmt.Errorf("invalid range: start or end line out of bounds")
		}

		// Split the lines that will be modified
		if start.Character > uint32(len(lines[start.Line])) {
			start.Character = uint32(len(lines[start.Line]))
		}

		// Handle single line changes
		if start.Line == end.Line {
			line := lines[start.Line]
			newLine := line[:start.Character] + change.Text + line[end.Character:]
			lines[start.Line] = newLine
		} else {
			// Handle multi-line changes
			startLine := lines[start.Line][:start.Character] + change.Text
			endLine := lines[end.Line][end.Character:]

			newLines := strings.Split(startLine, "\n")
			newLines[len(newLines)-1] += endLine

			// Replace the affected lines
			lines = append(lines[:start.Line], append(newLines, lines[end.Line+1:]...)...)
		}
	}

	return strings.Join(lines, "\n"), nil
}
