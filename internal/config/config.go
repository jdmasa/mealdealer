// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime settings.
type Config struct {
	BaseURL        string  // OpenAI-compatible API base URL
	APIKey         string  // API key
	ChatModel      string  // model for suggestions (text chat completions)
	ExtractModel   string  // model for text/PDF extraction (may equal ChatModel)
	VisionModel    string  // model for image understanding (may equal ChatModel)
	EmbeddingModel string  // model for embeddings
	DBPath         string  // SQLite file path
	Port           string  // HTTP listen port
	OutputLang     string  // default output language: "ca" or "es"
	SeasonWeight   float64 // weight of seasonal proximity in retrieval ranking
	RequestTimeout int     // seconds; HTTP read/write timeout (local LLMs can be slow)
}

// Load reads configuration from the environment, applying sensible defaults.
func Load() Config {
	c := Config{
		BaseURL:        env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		APIKey:         env("OPENAI_API_KEY", ""),
		ChatModel:      env("CHAT_MODEL", "gpt-4o-mini"),
		ExtractModel:   env("EXTRACT_MODEL", ""),
		VisionModel:    env("VISION_MODEL", ""),
		EmbeddingModel: env("EMBEDDING_MODEL", "text-embedding-3-small"),
		DBPath:         env("DB_PATH", "data/mealplanner.db"),
		Port:           env("PORT", "8080"),
		OutputLang:     env("OUTPUT_LANG", "ca"),
		SeasonWeight:   envFloat("SEASON_WEIGHT", 0.15),
		RequestTimeout: envInt("REQUEST_TIMEOUT", 300),
	}
	// Default the extraction and vision models to the chat model when not set.
	if strings.TrimSpace(c.ExtractModel) == "" {
		c.ExtractModel = c.ChatModel
	}
	if strings.TrimSpace(c.VisionModel) == "" {
		c.VisionModel = c.ChatModel
	}
	return c
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
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
