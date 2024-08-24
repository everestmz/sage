package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

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
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	numFiles := 0

	ctx, server, closeLsp, err := startLsp(PyrightLangserver, wd)
	if err != nil {
		panic(err)
	}

	db, err := openDB(wd)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	insertCh := make(chan *SymbolInfo, 1000)
	defer close(insertCh)

	go func() {
		tick := time.NewTicker(time.Second)
		var lastId int64 = 0
		var lastPrinted int64 = 0
		for {
			select {
			case info, ok := <-insertCh:
				if !ok {
					return
				}

				start := info.Location.Range.Start
				end := info.Location.Range.End
				id, err := db.InsertSymbol(
					info.Kind.String(), info.Name, info.RelativePath,
					int(start.Line), int(start.Character),
					int(end.Line), int(end.Character),
				)
				if err != nil {
					panic(err)
				}
				lastId = id
			case <-tick.C:
				if lastId == lastPrinted {
					continue
				}
				lastPrinted = lastId
				fmt.Println(lastPrinted)
			}
		}
	}()

	numSymbols := 0
	resetCounter := 0

	filepath.WalkDir(wd, func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}

		if filepath.Ext(path) == ".py" {
			numFiles++

			ctx, cancel := context.WithTimeout(ctx, time.Minute*10)
			defer cancel()

			pathUri := uri.File(path)
			fileContents, err := os.ReadFile(path)
			if err != nil {
				panic(err)
			}

			textDoc := protocol.TextDocumentItem{
				URI:        pathUri,
				LanguageID: protocol.PythonLanguage,
				Version:    1,
				Text:       string(fileContents),
			}

			relativePath, err := filepath.Rel(wd, path)
			if err != nil {
				panic(err)
			}

			fmt.Println("Evaluating", relativePath)
			start := time.Now()
			err = server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
				TextDocument: textDoc,
			})
			if err != nil {
				panic(err)
			}

			syms, err := server.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: pathUri,
				},
			})
			if err != nil {
				panic(err)
			}

			err = server.DidClose(ctx, &protocol.DidCloseTextDocumentParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: textDoc.URI,
				},
			})
			if err != nil {
				panic(err)
			}

			for _, sym := range syms {
				info := &protocol.SymbolInformation{}
				bs, _ := json.Marshal(sym)
				err = json.Unmarshal(bs, info)
				if err != nil {
					panic(err)
				}

				insertCh <- &SymbolInfo{
					SymbolInformation: *info,
					RelativePath:      relativePath,
				}
			}
			numSymbols += len(syms)
			resetCounter += len(syms)

			if resetCounter > 200000 {
				resetCounter = 0
				closeLsp()
				ctx, server, closeLsp, err = startLsp(PyrightLangserver, wd)
				if err != nil {
					panic(err)
				}
			}

			fmt.Println(fmt.Sprintf("%d (total %d)", len(syms), numSymbols), "symbols in", time.Since(start))
		}

		return nil
	})

	err = server.Exit(ctx)
	if err != nil {
		panic(err)
	}

	return
}

func startLsp(lsp *LanguageServerConfig, folder string) (context.Context, protocol.Server, func(), error) {
	cmd := exec.Command(lsp.Command, lsp.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
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

	lspClient := protocol.ClientDispatcher(conn, zap.L())

	ctx, conn, server := protocol.NewClient(context.Background(), lspClient, stream, zap.L())

	result, err := server.Initialize(ctx, &protocol.InitializeParams{
		ProcessID:    int32(os.Getpid()),
		Capabilities: protocol.ClientCapabilities{},
		WorkspaceFolders: []protocol.WorkspaceFolder{
			{
				URI:  string(uri.File(folder)),
				Name: "Main workspace",
			},
		},
	})
	if err != nil {
		return nil, nil, nil, err
	}

	fmt.Println("Setting config...")
	err = server.DidChangeConfiguration(ctx, &protocol.DidChangeConfigurationParams{
		Settings: map[string]any{},
	})
	if err != nil {
		return nil, nil, nil, err
	}
	fmt.Println("Config set!")

	fmt.Println(result.Capabilities.DocumentSymbolProvider)

	return ctx, server, func() {
		conn.Close()
	}, nil
}
