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
	"time"

	"github.com/everestmz/llmcat/treesym"
	"github.com/everestmz/llmcat/treesym/language"
	"github.com/everestmz/sage/lsp"
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
	Use:   "index",
	Short: "Create or update the sage index for a directory",
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

			syms, err := indexFile(wd, filePath, content, config, llm, indexFileOptions...)
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
				log.Info().Str("path", path).Str("hash", hashStr).Msg("Skipping indexed file")
				return nil
			}

			log.Debug().Str("path", path).Str("hash", hashStr).Msg("Indexing file")
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

			syms, err := indexFile(wd, path, content, config, llm, indexFileOptions...)
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

type IndexFileOption int

const (
	IncludeEmbeddings IndexFileOption = iota
)

func indexFile(wd, path string, content []byte, config *SagePathConfig, llm *LLMClient, options ...IndexFileOption) ([]*SymbolInfo, error) {
	var shouldEmbed bool
	for _, opt := range options {
		switch opt {
		case IncludeEmbeddings:
			shouldEmbed = true
		default:
			panic("Unhandled indexing option: " + fmt.Sprint(opt))
		}
	}

	relativePath, err := filepath.Rel(wd, path)
	if err != nil {
		return nil, err
	}

	fileUri := uri.File(relativePath)

	processedFile, err := treesym.GetSymbols(context.TODO(), &treesym.SourceFile{
		Path: path,
		Text: string(content),
	})
	if err == language.ErrUnsupportedExtension {
		log.Debug().Msgf("No supported tree-sitter grammar for file %s", path)
		return nil, nil
	} else if err != nil {
		// TODO: some kind of partial functionality?
		log.Error().Err(err).Msgf("Unable to extract symbols from %s", path)
		return nil, nil
	}

	syms := processedFile.Symbols.Definitions

	var result []*SymbolInfo

	for _, sym := range syms {
		calculatedInfo := &SymbolInfo{
			SymbolInformation: protocol.SymbolInformation{
				Name:       sym.Name,
				Kind:       lsp.TreeSymKindToLspKind(sym.Kind),
				Tags:       []protocol.SymbolTag{},
				Deprecated: false,
				Location: protocol.Location{
					URI: fileUri,
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      sym.StartPoint.Row,
							Character: sym.StartPoint.Column,
						},
						End: protocol.Position{
							Line:      sym.EndPoint.Row,
							Character: sym.EndPoint.Column,
						},
					},
				},
				// Empty container name since we're only getting top-level right now
				ContainerName: "",
			},
			RelativePath: relativePath,
		}

		if shouldEmbed {
			symbolText := lsp.GetRangeFromFile(string(content), calculatedInfo.Location.Range)

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
			calculatedInfo.Embedding = nil
		}

		result = append(result, calculatedInfo)
	}

	return result, nil
}
