// Package llm wraps an OpenAI-compatible API for chat, vision, and embeddings.
package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	"mealplanner/internal/config"
)

// Client talks to an OpenAI-compatible endpoint. Chat/vision use one endpoint;
// embeddings may use a separate endpoint AND a separate protocol — either the
// OpenAI /embeddings API or Hugging Face's feature-extraction API.
type Client struct {
	api            *openai.Client // chat, extraction, vision
	embApi         *openai.Client // OpenAI-protocol embeddings
	httpc          *http.Client   // raw HTTP (HF feature-extraction, probes)
	chatModel      string
	extractModel   string
	visionModel    string
	embeddingModel string
	queryPrefix    string // prepended to query text before embedding
	docPrefix      string // prepended to stored text before embedding
	embProvider    string // "openai" | "hf"
	embBaseURL     string // trimmed embeddings base URL
	embAPIKey      string
}

// New builds a client from config.
func New(cfg config.Config) *Client {
	oc := openai.DefaultConfig(cfg.APIKey)
	oc.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	ec := openai.DefaultConfig(cfg.EmbeddingAPIKey)
	ec.BaseURL = strings.TrimRight(cfg.EmbeddingBaseURL, "/")

	return &Client{
		api:            openai.NewClientWithConfig(oc),
		embApi:         openai.NewClientWithConfig(ec),
		httpc:          &http.Client{},
		chatModel:      cfg.ChatModel,
		extractModel:   cfg.ExtractModel,
		visionModel:    cfg.VisionModel,
		embeddingModel: cfg.EmbeddingModel,
		queryPrefix:    cfg.EmbedQueryPrefix,
		docPrefix:      cfg.EmbedDocPrefix,
		embProvider:    cfg.EmbeddingProvider,
		embBaseURL:     strings.TrimRight(cfg.EmbeddingBaseURL, "/"),
		embAPIKey:      cfg.EmbeddingAPIKey,
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

// Ping sends a minimal chat completion to verify the chat endpoint responds.
// Used as a startup connectivity fallback when /models isn't available.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.api.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:     c.chatModel,
		Messages:  []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "ping"}},
		MaxTokens: 1,
	})
	return err
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

// EmbeddingID identifies the embedding vector space (provider + model). Vectors
// from different IDs are not comparable; the store tags and filters by this.
func (c *Client) EmbeddingID() string {
	return c.embProvider + "/" + c.embeddingModel
}

// EmbedDocument embeds text destined for storage/retrieval (applies the
// document task prefix, e.g. Nomic's "search_document: ").
func (c *Client) EmbedDocument(ctx context.Context, text string) ([]float32, error) {
	return c.Embed(ctx, c.docPrefix+text)
}

// EmbedQuery embeds a search/query text (applies the query task prefix, e.g.
// Nomic's "search_query: ").
func (c *Client) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return c.Embed(ctx, c.queryPrefix+text)
}

// Embed returns the embedding vector for a single text, exactly as given (no
// task prefix). Prefer EmbedDocument/EmbedQuery for RAG. Routes to the OpenAI
// /embeddings API or the Hugging Face feature-extraction API per configuration.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if c.embProvider == "hf" {
		return c.hfEmbed(ctx, text)
	}
	resp, err := c.embApi.CreateEmbeddings(ctx, openai.EmbeddingRequest{
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

// hfEmbed calls Hugging Face's feature-extraction API, e.g.
// {base}/models/{model}/pipeline/feature-extraction with body {"inputs": text}.
// The response is a raw float array ([dim]) or token matrix ([tokens][dim]);
// a matrix is mean-pooled to a single sentence vector.
func (c *Client) hfEmbed(ctx context.Context, text string) ([]float32, error) {
	url := c.embBaseURL + "/models/" + c.embeddingModel + "/pipeline/feature-extraction"
	payload, _ := json.Marshal(map[string]any{
		"inputs":  text,
		"options": map[string]any{"wait_for_model": true},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("hf embeddings: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.embAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.embAPIKey)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hf embeddings: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hf embeddings: HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	return parseHFEmbedding(body)
}

// parseHFEmbedding accepts a 1-D vector or a 2-D token matrix (which it mean-pools).
func parseHFEmbedding(body []byte) ([]float32, error) {
	var one []float32
	if err := json.Unmarshal(body, &one); err == nil && len(one) > 0 {
		return one, nil
	}
	var two [][]float32
	if err := json.Unmarshal(body, &two); err == nil && len(two) > 0 && len(two[0]) > 0 {
		dim := len(two[0])
		out := make([]float32, dim)
		for _, row := range two {
			for i := 0; i < dim && i < len(row); i++ {
				out[i] += row[i]
			}
		}
		for i := range out {
			out[i] /= float32(len(two))
		}
		return out, nil
	}
	return nil, fmt.Errorf("hf embeddings: unexpected response shape: %s", snippet(body))
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
