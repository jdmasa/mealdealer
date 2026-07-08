// Package llm wraps an OpenAI-compatible API for chat, vision, and embeddings.
package llm

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	"mealplanner/internal/config"
)

// Client talks to an OpenAI-compatible endpoint.
type Client struct {
	api            *openai.Client
	chatModel      string
	extractModel   string
	visionModel    string
	embeddingModel string
}

// New builds a client from config.
func New(cfg config.Config) *Client {
	oc := openai.DefaultConfig(cfg.APIKey)
	oc.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{
		api:            openai.NewClientWithConfig(oc),
		chatModel:      cfg.ChatModel,
		extractModel:   cfg.ExtractModel,
		visionModel:    cfg.VisionModel,
		embeddingModel: cfg.EmbeddingModel,
	}
}

// ChatJSON sends a system+user prompt using the chat model (used for
// suggestions) and returns the assistant's text as JSON.
func (c *Client) ChatJSON(ctx context.Context, system, user string) (string, error) {
	return c.chatJSON(ctx, c.chatModel, system, user)
}

// ExtractJSON is like ChatJSON but uses the (possibly larger) extraction model,
// for parsing uploaded menus into structured JSON.
func (c *Client) ExtractJSON(ctx context.Context, system, user string) (string, error) {
	return c.chatJSON(ctx, c.extractModel, system, user)
}

// chatJSON requests a JSON object response from the given model. It transparently
// retries once without the response_format hint for servers that don't support it.
func (c *Client) chatJSON(ctx context.Context, model, system, user string) (string, error) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: system},
		{Role: openai.ChatMessageRoleUser, Content: user},
	}
	req := openai.ChatCompletionRequest{
		Model:       model,
		Messages:    msgs,
		Temperature: 0.4,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	}
	resp, err := c.api.CreateChatCompletion(ctx, req)
	if err != nil {
		// Retry without the JSON response_format for stricter-compatible servers.
		req.ResponseFormat = nil
		resp, err = c.api.CreateChatCompletion(ctx, req)
		if err != nil {
			return "", fmt.Errorf("chat completion: %w", err)
		}
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("chat completion: empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

// VisionJSON sends an image plus an instruction and returns the assistant's
// text (expected to be JSON).
func (c *Client) VisionJSON(ctx context.Context, system, instruction string, img []byte, mime string) (string, error) {
	if mime == "" {
		mime = "image/jpeg"
	}
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: system},
		{
			Role: openai.ChatMessageRoleUser,
			MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeText, Text: instruction},
				{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{
					URL:    dataURL,
					Detail: openai.ImageURLDetailAuto,
				}},
			},
		},
	}
	resp, err := c.api.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:       c.visionModel,
		Messages:    msgs,
		Temperature: 0.2,
	})
	if err != nil {
		return "", fmt.Errorf("vision completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("vision completion: empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

// ListModels queries the provider's /models endpoint and returns the available
// model IDs. Used at startup as a connectivity check.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	list, err := c.api.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list.Models))
	for _, m := range list.Models {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// Embed returns the embedding vector for a single text.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := c.api.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: []string{text},
		Model: openai.EmbeddingModel(c.embeddingModel),
	})
	if err != nil {
		return nil, fmt.Errorf("embeddings: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("embeddings: empty response")
	}
	return resp.Data[0].Embedding, nil
}
