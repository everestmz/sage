package main

import (
	"context"
	"path/filepath"

	ollama "github.com/ollama/ollama/api"
	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

var llmLogger = zerolog.New(&lumberjack.Logger{
	Filename:   filepath.Join(getConfigDir(), "llm.log"),
	MaxSize:    50,
	MaxBackups: 10,
}).With().Timestamp().Logger()

type LLMClient struct {
	ol *ollama.Client
}

func NewLLMClient() (*LLMClient, error) {
	ol, err := ollama.ClientFromEnvironment()
	if err != nil {
		return nil, err
	}

	return &LLMClient{
		ol: ol,
	}, nil
}

type CompletionResponse struct {
	Text string
	Done bool
}

type GenerateResponseFunc = func(CompletionResponse) error

func (lc *LLMClient) StreamCompletion(ctx context.Context, model, text string, handler GenerateResponseFunc) error {
	stream := true

	output := ""
	defer func(resp *string) {
		llmLogger.Info().
			Str("model", model).
			Str("prompt", text).
			Str("response", *resp).
			Msg("Finished streaming completion")
	}(&output)

	return lc.ol.Generate(ctx, &ollama.GenerateRequest{
		Model:  model,
		Prompt: text,
		Stream: &stream,
	}, func(gr ollama.GenerateResponse) error {
		output += gr.Response
		return handler(CompletionResponse{
			Text: gr.Response,
			Done: gr.Done,
		})
	})
}

func (lc *LLMClient) GenerateCompletion(ctx context.Context, model, text string) (string, error) {
	stream := false

	var completion string

	err := lc.ol.Generate(ctx, &ollama.GenerateRequest{
		Model:  model,
		Prompt: text,
		Stream: &stream,
	}, func(gr ollama.GenerateResponse) error {
		completion = gr.Response
		return nil
	})

	llmLogger.Info().
		Str("model", model).
		Str("prompt", text).
		Str("response", completion).
		Msg("Finished completion")

	return completion, err
}

func (lc *LLMClient) GetEmbedding(ctx context.Context, model, text string) ([]float64, error) {
	resp, err := lc.ol.Embeddings(ctx, &ollama.EmbeddingRequest{
		Model:  model,
		Prompt: text,
	})
	if err != nil {
		return nil, err
	}

	return resp.Embedding, nil
}
