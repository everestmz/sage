package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func init() {
	flags := IndexCmd.PersistentFlags()

	flags.StringP("file", "f", "", "specify a specific file to index for debugging/information purposes")
	flags.BoolP("embed", "e", false, "specify whether or not to generate embeddings for symbols")
}

var IndexCmd = &cobra.Command{
	Use: "index",
	RunE: func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		config, err := getConfigForWd()
		if err != nil {
			return err
		}

		flags := cmd.Flags()

		shouldEmbed, err := flags.GetBool("embed")
		if err != nil {
			return err
		}

		indexFileOptions := []IndexFileOption{}
		if shouldEmbed {
			indexFileOptions = append(indexFileOptions, IncludeEmbeddings)
		}

		numFiles := 0

		lsps := map[string]*ChildLanguageServer{}

		params := &protocol.InitializeParams{
			ProcessID:    int32(os.Getpid()),
			Capabilities: protocol.ClientCapabilities{},
			WorkspaceFolders: []protocol.WorkspaceFolder{
				{
					URI:  string(uri.File(wd)),
					Name: "Main workspace",
				},
			},
		}
		for languageName, config := range config.Languages {
			ls, err := startLsp(config.LanguageServer, nil, params)
			if err != nil {
				panic(err)
			}

			fmt.Println("Setting config...")
			err = ls.Server.DidChangeConfiguration(context.TODO(), &protocol.DidChangeConfigurationParams{
				Settings: map[string]any{},
			})
			if err != nil {
				return err
			}
			fmt.Println("Config set!")

			defer func() {
				err = ls.Exit(ls.Context)
				if err != nil {
					panic(err)
				}
			}()

			lsps[languageName] = ls
		}

		llm, err := NewLLMClient()
		if err != nil {
			return err
		}

		db, err := openDB(wd)
		if err != nil {
			panic(err)
		}
		defer db.Close()

		insertCh := make(chan *SymbolInfo, 1000)
		defer close(insertCh)

		filePath, err := flags.GetString("file")
		if err != nil {
			return err
		}

		if filePath != "" {
			content, err := os.ReadFile(filePath)
			if err != nil {
				return err
			}

			relativePath, err := filepath.Rel(wd, filePath)
			if err != nil {
				return err
			}

			name, langConfig := config.GetLanguageConfigForFile(relativePath)
			if langConfig == nil {
				return fmt.Errorf("No language configuration in '%s' matching path '%s'", config.Name(), relativePath)
			}

			ls := lsps[name]

			syms, err := indexFile(wd, filePath, content, config, ls, llm, indexFileOptions...)
			if err != nil {
				return err
			}

			bs, _ := json.MarshalIndent(map[string]any{
				"symbols": syms,
			}, "", "\t")
			fmt.Println(string(bs))
			return nil
		}

		numSymbols := 0
		resetCounter := 0

		var timedOutFiles []string

		indexFunc := func(path string) error {
			configName, languageConfig := config.GetLanguageConfigForFile(path)
			if languageConfig == nil {
				return nil
			}

			ls := lsps[configName]

			log.Debug().Str("path", path).Msg("Inspecting file")
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			hash := md5.Sum(content)
			hashStr := fmt.Sprintf("%x", hash)
			_, ok, err := db.GetFile(path, hashStr)
			if err != nil {
				return fmt.Errorf("Error getting file: %w", err)
			}
			if ok {
				// We've already indexed this exact file
				log.Debug().Str("path", path).Str("hash", hashStr).Msg("Skipping indexed file")
				return nil
			}

			tx, err := db.Begin()
			if err != nil {
				return err
			}
			defer func() {
				err := tx.Commit()
				if err != nil {
					panic(err)
				}
			}()

			// Clear out any old info for this file
			err = tx.DeleteFileInfoByPath(path)
			if err != nil {
				return fmt.Errorf("Error deleting file: %w", err)
			}

			fileId, err := tx.InsertFile(path, hashStr)
			if err != nil {
				return fmt.Errorf("Error inserting file: %w", err)
			}

			start := time.Now()

			syms, err := indexFile(wd, path, content, config, ls, llm, indexFileOptions...)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					timedOutFiles = append(timedOutFiles, path)
				} else {
					return err
				}
			}
			numSyms := len(syms)

			for _, sym := range syms {
				start := sym.Location.Range.Start
				end := sym.Location.Range.End
				_, err := tx.InsertSymbol(fileId,
					float64(sym.Kind), sym.Name, sym.RelativePath,
					int(start.Line), int(start.Character),
					int(end.Line), int(end.Character), sym.Embedding,
				)
				if err != nil {
					return err
				}
			}

			numFiles++

			numSymbols += numSyms
			resetCounter += numSyms

			if resetCounter > 200000 {
				resetCounter = 0
				ls.Close()
				ls, err = startLsp(languageConfig.LanguageServer, nil, params)
				if err != nil {
					panic(err)
				}

				fmt.Println("Setting config...")
				err = ls.Server.DidChangeConfiguration(context.TODO(), &protocol.DidChangeConfigurationParams{
					Settings: map[string]any{},
				})
				if err != nil {
					return err
				}
				fmt.Println("Config set!")
				lsps[configName] = ls
			}

			fmt.Println(fmt.Sprintf("%d (total %d)", numSyms, numSymbols), "symbols in", time.Since(start))

			return nil
		}

		walkFunc := func(path string, d fs.DirEntry, err error) error {
			if d.IsDir() {
				return nil
			}

			shortPath, err := filepath.Rel(wd, path)
			if err != nil {
				return err
			}

			// This will be a no-op if there are no excludes
			for i, exc := range config.compiledExcludes {
				if exc.Match(shortPath) {
					log.Debug().Str("path", shortPath).Str("pattern", config.Exclude[i]).Msg("Skipping file because of match with excludes")
					return nil
				}
			}

			return indexFunc(path)
		}

		if len(config.Include) > 0 {
			// We should walk the includes if they're directories, and parse them if they're strings
			for _, include := range config.Include {
				matches, err := filepath.Glob(include)
				if err != nil {
					return err
				}

				for _, match := range matches {
					info, err := os.Stat(match)
					if err != nil {
						return err
					}

					absolute, err := filepath.Abs(filepath.Join(wd, match))
					if err != nil {
						return err
					}

					if info.IsDir() {
						err = filepath.WalkDir(absolute, walkFunc)
						if err != nil {
							return err
						}
					} else {
						err = indexFunc(absolute)
						if err != nil {
							return err
						}
					}
				}
			}
		} else {
			// We just walk everything, ignoring excludes if they exist
			err = filepath.WalkDir(wd, walkFunc)
		}

		if len(timedOutFiles) > 0 {
			fmt.Println("Timeouts recorded when indexing the following paths:")
			for _, path := range timedOutFiles {
				fmt.Println("-", path)
			}

		}
		return err
	},
}

func getRangeFromFile(text string, locationRange protocol.Range) string {
	lines := strings.Split(text, "\n")

	var snippetLines []string

	start := locationRange.Start
	end := locationRange.End

	snippetLines = append(snippetLines, lines[start.Line][start.Character:])

	for i := start.Line + 1; i < end.Line; i++ {
		snippetLines = append(snippetLines, lines[i])
	}

	snippetLines = append(snippetLines, lines[end.Line][:end.Character])

	return strings.Join(snippetLines, "\n")
}

type IndexFileOption int

const (
	IncludeEmbeddings IndexFileOption = iota
)

func indexFile(wd, path string, content []byte, config *SagePathConfig, ls *ChildLanguageServer, llm *LLMClient, options ...IndexFileOption) ([]*SymbolInfo, error) {
	var shouldEmbed bool
	for _, opt := range options {
		switch opt {
		case IncludeEmbeddings:
			shouldEmbed = true
		default:
			panic("Unhandled indexing option: " + fmt.Sprint(opt))
		}
	}

	ctx, cancel := context.WithTimeout(ls.Context, time.Minute)
	defer cancel()

	// TODO: make this a config-driven option
	// model := "deepseek-coder-v2:16b"

	pathUri := uri.File(path)

	textDoc := protocol.TextDocumentItem{
		URI:        pathUri,
		LanguageID: protocol.PythonLanguage,
		Version:    1,
		Text:       string(content),
	}

	relativePath, err := filepath.Rel(wd, path)
	if err != nil {
		return nil, err
	}

	err = ls.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: textDoc,
	})
	if err != nil {
		return nil, err
	}

	syms, err := ls.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: pathUri,
		},
	})
	if err != nil {
		return nil, err
	}

	err = ls.DidClose(ctx, &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: textDoc.URI,
		},
	})
	if err != nil {
		return nil, err
	}

	var result []*SymbolInfo

	for _, sym := range syms {
		info := &protocol.SymbolInformation{}
		bs, _ := json.Marshal(sym)
		err = json.Unmarshal(bs, info)
		if err != nil {
			return nil, err
		}

		if info.ContainerName != "" && info.Kind >= protocol.SymbolKindVariable {
			continue
		}

		calculatedInfo := &SymbolInfo{
			SymbolInformation: *info,
			RelativePath:      relativePath,
		}

		// fmt.Println("Indexing", info.Kind, info.Name)

		if shouldEmbed {
			symbolText := getRangeFromFile(string(content), info.Location.Range)

			prompt := "\n============= INSTRUCTIONS: ================\nDescribe the following code. Be concise, but descriptive. Use domain-specific terms like function names, class names, etc. Make sure you cover as many edge cases and interesting codepaths/features as possible. Above this message is more code from the same file as this symbol. Your response will be indexed for full-text search. DO NOT reproduce the code with comments. Simply write a short but descriptive paragraph about the code:\n"

			prompt += "File: " + path + "\n"

			var explanation string

			// if len(strings.Split(symbolText, "\n")) > 2 {
			// 	explanation, err = llm.GenerateCompletion(context.TODO(), *config.Models.ExplainCode, textDoc.Text+prompt+symbolText)
			// 	if err != nil {
			// 		return nil, err
			// 	}
			// }

			prompt = explanation + "\n" + symbolText

			// fmt.Fprintln(os.Stderr, prompt)

			models, err := config.Models.Get()
			if err != nil {
				return nil, err
			}

			embedding, err := llm.GetEmbedding(context.TODO(), *models.Embedding, prompt)
			if err != nil {
				return nil, err
			}

			calculatedInfo.Embedding = embedding
		} else {
			calculatedInfo.Embedding = make([]float64, 768)
		}

		result = append(result, calculatedInfo)
	}

	return result, nil
}
