package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	cursor "github.com/everestmz/cursor-rpc"
	aiserverv1 "github.com/everestmz/cursor-rpc/cursor/gen/aiserver/v1"

	"github.com/rs/zerolog"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

var lspCommands = []*CommandDefinition{
	lspCommandExecCursorCompletion,
	lspCommandExecCompletion,
	lspCommandOpenModelsConfig,
	lspCommandOpenContextConfig,
	lspCommandShowCurrentContext,
	lspCommandShowCurrentModel,
}

func getPositionOffset(text string) protocol.Position {
	newLines := strings.Count(text, "\n")
	var cols = len(text)
	if newLines > 0 {
		spl := strings.Split(text, "\n")
		cols = len(spl[newLines])
	}

	return protocol.Position{
		Line:      uint32(newLines),
		Character: uint32(cols),
	}
}

var lspCommandShowCurrentModel = &CommandDefinition{
	Title:          "Show current Ollama model",
	ShowCodeAction: false,
	Identifier:     "sage.workspace.configuration.model",
	BuildArgs: func(params *protocol.CodeActionParams) ([]any, error) {
		return []any{}, nil
	},
	Execute: func(params *protocol.ExecuteCommandParams, client LspClient, clientInfo *LanguageServerClientInfo) (*protocol.ApplyWorkspaceEditParams, error) {
		client.Progress(context.TODO(), &protocol.ProgressParams{
			// Token: *params.WorkDoneProgressParams.WorkDoneToken,
			Value: &protocol.WorkDoneProgressBegin{
				Kind:    protocol.WorkDoneProgressKindBegin,
				Title:   "Ollama model",
				Message: "getting model...",
			},
		})

		models, err := clientInfo.Config.Models.Get()
		if err != nil {
			return nil, err
		}

		client.Progress(context.TODO(), &protocol.ProgressParams{
			// Token: *params.WorkDoneProgressParams.WorkDoneToken,
			Value: &protocol.WorkDoneProgressEnd{
				Kind:    protocol.WorkDoneProgressKindEnd,
				Message: *models.Default,
			},
		})

		return nil, nil
	},
}

var lspCommandShowCurrentContext = &CommandDefinition{
	Title:          "Show context",
	ShowCodeAction: false,
	Identifier:     "sage.workspace.context.show",
	BuildArgs: func(params *protocol.CodeActionParams) ([]any, error) {
		return []any{}, nil
	},
	Execute: func(params *protocol.ExecuteCommandParams, client LspClient, clientInfo *LanguageServerClientInfo) (*protocol.ApplyWorkspaceEditParams, error) {
		providers, err := clientInfo.Config.Context.Get()
		if err != nil {
			return nil, err
		}

		llmContext, err := BuildContext(providers, clientInfo)
		if err != nil {
			return nil, err
		}

		f, err := os.CreateTemp(os.TempDir(), "sage_context")
		if err != nil {
			return nil, err
		}

		_, err = f.WriteString(llmContext)
		if err != nil {
			return nil, err
		}

		filename := f.Name()

		f.Close()

		result := &protocol.ShowDocumentResult{}
		_, err = client.Conn().Call(context.TODO(), string(protocol.MethodShowDocument), protocol.ShowDocumentParams{
			URI:       uri.File(filename),
			External:  false,
			TakeFocus: true,
			Selection: nil,
		}, result)

		return nil, err
	},
}

var lspCommandOpenContextConfig = &CommandDefinition{
	Title:          "Edit context",
	ShowCodeAction: true,
	Identifier:     "sage.workspace.context.edit",
	BuildArgs: func(params *protocol.CodeActionParams) ([]any, error) {
		return []any{}, nil
	},
	Execute: func(params *protocol.ExecuteCommandParams, client LspClient, clientInfo *LanguageServerClientInfo) (*protocol.ApplyWorkspaceEditParams, error) {
		result := &protocol.ShowDocumentResult{}
		_, err := client.Conn().Call(context.TODO(), string(protocol.MethodShowDocument), protocol.ShowDocumentParams{
			URI:       uri.File(getWorkspaceContextPath()),
			External:  false,
			TakeFocus: true,
			Selection: nil,
		}, result)

		return nil, err
	},
}

var lspCommandOpenModelsConfig = &CommandDefinition{
	Title:          "Edit workspace configuration",
	ShowCodeAction: true,
	Identifier:     "sage.workspace.configuration.edit",
	BuildArgs: func(params *protocol.CodeActionParams) ([]any, error) {
		return []any{}, nil
	},
	Execute: func(params *protocol.ExecuteCommandParams, client LspClient, clientInfo *LanguageServerClientInfo) (*protocol.ApplyWorkspaceEditParams, error) {
		result := &protocol.ShowDocumentResult{}
		_, err := client.Conn().Call(context.TODO(), string(protocol.MethodShowDocument), protocol.ShowDocumentParams{
			URI:       uri.File(getWorkspaceModelsPath()),
			External:  false,
			TakeFocus: true,
			Selection: nil,
		}, result)

		return nil, err
	},
}

var lspCommandExecCursorCompletion = &CommandDefinition{
	Title:          "Cursor generate",
	ShowCodeAction: true,
	Identifier:     "sage.completion.cursor.selection",
	BuildArgs: func(params *protocol.CodeActionParams) ([]any, error) {
		args := &LlmCompletionArgs{
			Filename:  params.TextDocument.URI,
			Selection: params.Range,
		}

		return []any{args}, nil
	},
	Execute: func(params *protocol.ExecuteCommandParams, client LspClient, clientInfo *LanguageServerClientInfo) (*protocol.ApplyWorkspaceEditParams, error) {
		lsLogger := globalLsLogger.With().Str("code_action", "sage.completion.selection").Logger()
		argBs, err := json.Marshal(params.Arguments[0])
		if err != nil {
			return nil, err
		}

		// TODO: move this out into some global initializer
		cursorCredentials, err := cursor.GetDefaultCredentials()
		if err != nil {
			return nil, err
		}

		args := &LlmCompletionArgs{}
		err = json.Unmarshal(argBs, args)
		if err != nil {
			return nil, err
		}

		prompt, err := buildPrompt(lsLogger, args, clientInfo)
		if err != nil {
			return nil, err
		}

		editsManager := NewLlmResponseEditsManager(client, args.Filename, args.Selection.End.Line, args.Selection.End.Character)

		aiClient := cursor.NewAiServiceClient()

		model := "claude-3.5-sonnet"

		resp, err := aiClient.StreamChat(context.TODO(), cursor.NewRequest(cursorCredentials, &aiserverv1.GetChatRequest{
			ModelDetails: &aiserverv1.ModelDetails{
				ModelName: &model,
			},
			Conversation: []*aiserverv1.ConversationMessage{
				{
					Text: prompt,
					Type: aiserverv1.ConversationMessage_MESSAGE_TYPE_HUMAN,
				},
			},
		}))

		if err != nil {
			return nil, err
		}

		for resp.Receive() {
			next := resp.Msg()
			editsManager.NextEdit(next.Text)
		}

		return nil, nil
	},
}

func buildPrompt(lsLogger zerolog.Logger, args *LlmCompletionArgs, clientInfo *LanguageServerClientInfo) (string, error) {
	textDocument := clientInfo.GetOpenDocument(args.Filename)

	documentLines := append(strings.Split(textDocument.Text, "\n"), "") // Unixy files end in \n
	lineRange := documentLines[args.Selection.Start.Line : args.Selection.End.Line+1]
	lsLogger.Debug().Str("Line range", fmt.Sprint(lineRange)).Msg("Lines Range")
	endLineIndex := args.Selection.End.Line - args.Selection.Start.Line
	lsLogger.Debug().Uint32("end idx", endLineIndex).Msg("End idx")
	lineRange[0] = lineRange[0][args.Selection.Start.Character:]
	lineRange[endLineIndex] = lineRange[endLineIndex][:args.Selection.End.Character]
	lsLogger.Debug().Str("Line range", fmt.Sprint(lineRange)).Msg("Lines Range after narrowing")

	documentContext := strings.Join(documentLines[:args.Selection.End.Line], "\n")
	selectionText := strings.Join(lineRange, "\n")

	contextProviders, err := clientInfo.Config.Context.Get()
	if err != nil {
		return "", err
	}

	filesContext, err := BuildContext(contextProviders, clientInfo)
	if err != nil {
		return "", err
	}

	prompt := filesContext

	prompt += "<CurrentFile path=\"" + args.Filename.Filename() + "\">\n"
	prompt += documentContext
	prompt += "\n</CurrentFile>\n"

	prompt += `<SystemPrompt>
A user's prompt, in the form of a question, or a description code to write, is shown below. Satisfy the user's prompt or question to the best of your ability. If asked to complete code, DO NOT type out any extra text, or backticks since your response will be appended to the end of the CurrentFile. DO NOT regurgitate the whole file. Simply return the new code, or the modified code.
</SystemPrompt>
`

	prompt += "<UserPrompt>\n"
	prompt += selectionText
	prompt += "\n</UserPrompt>\n"

	return prompt, nil
}

type LlmResponseEditsManager struct {
	placeNextEdit protocol.Position
	fullText      string
	currentLine   string
	client        LspClient
	filename      uri.URI
}

func (m *LlmResponseEditsManager) NextEdit(nextText string) {
	m.fullText += nextText

	if strings.Contains(nextText, "\n") {
		spl := strings.Split(nextText, "\n")
		m.currentLine += spl[0]

		m.client.Progress(context.TODO(), &protocol.ProgressParams{
			// Token: *params.WorkDoneProgressParams.WorkDoneToken,
			Value: &protocol.WorkDoneProgressReport{
				Kind:    protocol.WorkDoneProgressKindReport,
				Message: "sage: " + m.currentLine,
			},
		})
		m.currentLine = strings.Join(spl[1:], "\n")
	} else {
		m.currentLine += nextText
	}

	m.client.ApplyEdit(context.TODO(), &protocol.ApplyWorkspaceEditParams{
		Label: "llm_line",
		Edit: protocol.WorkspaceEdit{
			Changes: map[uri.URI][]protocol.TextEdit{
				m.filename: []protocol.TextEdit{
					{
						Range: protocol.Range{
							Start: m.placeNextEdit,
							End:   m.placeNextEdit,
						},
						NewText: nextText,
					},
				},
			},
		},
	})
	offset := getPositionOffset(nextText)
	m.placeNextEdit.Line += offset.Line
	m.placeNextEdit.Character += offset.Character
	if offset.Line > 0 {
		m.placeNextEdit.Character = offset.Character
	}

}

func NewLlmResponseEditsManager(client LspClient, filename uri.URI, startLine, startChar uint32) *LlmResponseEditsManager {
	return &LlmResponseEditsManager{
		placeNextEdit: protocol.Position{
			Line:      startLine,
			Character: startChar,
		},
		filename: filename,
		client:   client,
	}
}

var lspCommandExecCompletion = &CommandDefinition{
	Title:          "Ollama generate",
	ShowCodeAction: true,
	Identifier:     "sage.completion.ollama.selection",
	BuildArgs: func(params *protocol.CodeActionParams) ([]any, error) {
		args := &LlmCompletionArgs{
			Filename:  params.TextDocument.URI,
			Selection: params.Range,
		}

		return []any{args}, nil
	},
	Execute: func(params *protocol.ExecuteCommandParams, client LspClient, clientInfo *LanguageServerClientInfo) (*protocol.ApplyWorkspaceEditParams, error) {
		lsLogger := globalLsLogger.With().Str("code_action", "sage.completion.selection").Logger()
		argBs, err := json.Marshal(params.Arguments[0])
		if err != nil {
			return nil, err
		}

		args := &LlmCompletionArgs{}
		err = json.Unmarshal(argBs, args)
		if err != nil {
			return nil, err
		}

		prompt, err := buildPrompt(lsLogger, args, clientInfo)
		if err != nil {
			return nil, err
		}

		completionCh := make(chan string)
		errCh := make(chan error)

		var receiveCompletionFunc GenerateResponseFunc = func(cr CompletionResponse) error {
			lsLogger.Debug().Str("text", cr.Text).Bool("done", cr.Done).Msg("Received text")
			completionCh <- cr.Text
			if cr.Done {
				close(completionCh)
			}

			return nil
		}

		models, err := clientInfo.Config.Models.Get()
		if err != nil {
			return nil, err
		}

		model := *models.Default

		lsLogger.Info().Str("model", model).Str("prompt", prompt).Msg("Generating completion")

		client.Progress(context.TODO(), &protocol.ProgressParams{
			// Token: *params.WorkDoneProgressParams.WorkDoneToken,
			Value: &protocol.WorkDoneProgressBegin{
				Kind:    protocol.WorkDoneProgressKindBegin,
				Title:   "Sage completion",
				Message: "connecting...",
			},
		})

		go func() {
			err := clientInfo.LLM.StreamCompletion(context.TODO(), model, prompt, receiveCompletionFunc)
			if err != nil {
				errCh <- err
			}

			close(errCh)
		}()

		editsManager := NewLlmResponseEditsManager(client, args.Filename, args.Selection.End.Line, args.Selection.End.Character)

	outer:
		for {
			select {
			case nextText, ok := <-completionCh:
				if !ok {
					break outer
				}

				editsManager.NextEdit(nextText)

			case err, ok := <-errCh:
				if !ok {
					continue
				}

				close(completionCh)

				return nil, err
			}
		}

		lsLogger.Debug().Str("completion", editsManager.fullText).Msg("Returning completion")
		client.Progress(context.TODO(), &protocol.ProgressParams{
			// Token: *params.WorkDoneProgressParams.WorkDoneToken,
			Value: &protocol.WorkDoneProgressEnd{
				Kind:    protocol.WorkDoneProgressKindEnd,
				Message: "Done!",
			},
		})

		return nil, nil
	},
}
