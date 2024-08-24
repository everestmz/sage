package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

var IndexCmd = &cobra.Command{
	Use: "index",
	RunE: func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		numFiles := 0

		ls, err := startLsp(PyrightLangserver, wd, nil, protocol.ClientCapabilities{})
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

				ctx, cancel := context.WithTimeout(ls.Context, time.Minute*10)
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
				err = ls.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
					TextDocument: textDoc,
				})
				if err != nil {
					panic(err)
				}

				syms, err := ls.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: pathUri,
					},
				})
				if err != nil {
					panic(err)
				}

				err = ls.DidClose(ctx, &protocol.DidCloseTextDocumentParams{
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
					ls.Close()
					ls, err = startLsp(PyrightLangserver, wd, nil, protocol.ClientCapabilities{})
					if err != nil {
						panic(err)
					}
				}

				fmt.Println(fmt.Sprintf("%d (total %d)", len(syms), numSymbols), "symbols in", time.Since(start))
			}

			return nil
		})

		err = ls.Exit(ls.Context)
		if err != nil {
			panic(err)
		}

		return nil
	},
}
