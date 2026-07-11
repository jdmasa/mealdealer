// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime settings.
type Config struct {
	BaseURL           string  // OpenAI-compatible API base URL (chat/extract/vision)
	APIKey            string  // API key for BaseURL
	EmbeddingProvider string  // "openai" (default) or "hf" (HF feature-extraction)
	EmbeddingBaseURL  string  // base URL for embeddings (defaults per provider)
	EmbeddingAPIKey   string  // API key for embeddings (defaults to APIKey)
	ChatModel         string  // model for suggestions (text chat completions)
	ExtractModel      string  // model for text/PDF extraction (may equal ChatModel)
	VisionModel       string  // model for image understanding (may equal ChatModel)
	EmbeddingModel    string  // model for embeddings
	EmbedQueryPrefix  string  // prepended to query text before embedding (e.g. Nomic "search_query: ")
	EmbedDocPrefix    string  // prepended to stored text before embedding (e.g. Nomic "search_document: ")
	DBPath            string  // SQLite file path
	Port              string  // HTTP listen port
	OutputLang        string  // default output language: "ca" or "es"
	SeasonWeight      float64 // weight of seasonal proximity in retrieval ranking
	RequestTimeout    int     // seconds; HTTP read/write timeout (local LLMs can be slow)
}

// Load reads configuration from the environment, applying sensible defaults.
func Load() Config {
	c := Config{
		BaseURL:           env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		APIKey:            env("OPENAI_API_KEY", ""),
		EmbeddingProvider: strings.ToLower(env("EMBEDDING_PROVIDER", "openai")),
		EmbeddingBaseURL:  env("EMBEDDING_BASE_URL", ""),
		EmbeddingAPIKey:   env("EMBEDDING_API_KEY", ""),
		ChatModel:         env("CHAT_MODEL", "gpt-4o-mini"),
		ExtractModel:      env("EXTRACT_MODEL", ""),
		VisionModel:       env("VISION_MODEL", ""),
		EmbeddingModel:    env("EMBEDDING_MODEL", "text-embedding-3-small"),
		// Prefixes are read verbatim (NOT trimmed) — the trailing space in
		// e.g. "search_query: " is significant.
		EmbedQueryPrefix: envRaw("EMBED_QUERY_PREFIX", ""),
		EmbedDocPrefix:   envRaw("EMBED_DOCUMENT_PREFIX", ""),
		DBPath:           env("DB_PATH", "data/mealplanner.db"),
		Port:             env("PORT", "8080"),
		OutputLang:       env("OUTPUT_LANG", "ca"),
		SeasonWeight:     envFloat("SEASON_WEIGHT", 0.15),
		RequestTimeout:   envInt("REQUEST_TIMEOUT", 300),
	}
	// Default the extraction and vision models to the chat model when not set.
	if strings.TrimSpace(c.ExtractModel) == "" {
		c.ExtractModel = c.ChatModel
	}
	if strings.TrimSpace(c.VisionModel) == "" {
		c.VisionModel = c.ChatModel
	}
	// Default the embeddings endpoint per provider.
	if c.EmbeddingProvider == "hf" {
		// HF feature-extraction lives under the hf-inference router base.
		if strings.TrimSpace(c.EmbeddingBaseURL) == "" {
			c.EmbeddingBaseURL = "https://router.huggingface.co/hf-inference"
		}
	} else {
		// OpenAI-compatible: reuse the main endpoint unless overridden.
		c.EmbeddingProvider = "openai"
		if strings.TrimSpace(c.EmbeddingBaseURL) == "" {
			c.EmbeddingBaseURL = c.BaseURL
		}
	}
	if strings.TrimSpace(c.EmbeddingAPIKey) == "" {
		c.EmbeddingAPIKey = c.APIKey
	}
	// Note: Nomic embedding models are trained with task prefixes
	// ("search_query: " / "search_document: "). Whether they must be added by the
	// caller depends on the serving layer — Hugging Face expects them, some Ollama
	// builds add them automatically (adding them twice hurts). So we do NOT apply
	// them automatically; set EMBED_QUERY_PREFIX / EMBED_DOCUMENT_PREFIX if needed.
	// main.go logs a hint when a nomic model has no prefix configured.
	return c
}

// LooksLikeNomic reports whether the embedding model appears to be a Nomic model
// (used only to surface a startup hint about task prefixes).
func (c Config) LooksLikeNomic() bool {
	return strings.Contains(strings.ToLower(c.EmbeddingModel), "nomic")
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envRaw returns the environment value verbatim (no trimming) when set and
// non-empty, else def. Used for values where surrounding spaces matter.
func envRaw(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
