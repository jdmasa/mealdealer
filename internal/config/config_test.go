package config

import "testing"

// clearEnv unsets every variable Load reads so each test starts clean.
func clearEnv(t *testing.T) {
	for _, k := range []string{
		"OPENAI_BASE_URL", "OPENAI_API_KEY", "EMBEDDING_BASE_URL", "EMBEDDING_API_KEY",
		"CHAT_MODEL", "EXTRACT_MODEL", "VISION_MODEL", "EMBEDDING_MODEL",
		"EMBED_QUERY_PREFIX", "EMBED_DOCUMENT_PREFIX",
	} {
		t.Setenv(k, "")
	}
}

func TestPrefixesNotAutoApplied(t *testing.T) {
	clearEnv(t)
	t.Setenv("EMBEDDING_MODEL", "nomic-embed-text")
	c := Load()
	if c.EmbedQueryPrefix != "" || c.EmbedDocPrefix != "" {
		t.Errorf("prefixes must NOT be auto-applied; got q=%q d=%q", c.EmbedQueryPrefix, c.EmbedDocPrefix)
	}
	if !c.LooksLikeNomic() {
		t.Errorf("LooksLikeNomic() = false for nomic-embed-text")
	}
}

func TestPrefixVerbatimPreservesTrailingSpace(t *testing.T) {
	clearEnv(t)
	t.Setenv("EMBED_QUERY_PREFIX", "search_query: ")
	t.Setenv("EMBED_DOCUMENT_PREFIX", "search_document: ")
	c := Load()
	if c.EmbedQueryPrefix != "search_query: " {
		t.Errorf("query prefix = %q, want the trailing space preserved", c.EmbedQueryPrefix)
	}
	if c.EmbedDocPrefix != "search_document: " {
		t.Errorf("doc prefix = %q, want the trailing space preserved", c.EmbedDocPrefix)
	}
}

func TestEmbeddingEndpointDefaultsToMain(t *testing.T) {
	clearEnv(t)
	t.Setenv("OPENAI_BASE_URL", "http://main:1/v1")
	t.Setenv("OPENAI_API_KEY", "mainkey")
	c := Load()
	if c.EmbeddingBaseURL != "http://main:1/v1" || c.EmbeddingAPIKey != "mainkey" {
		t.Errorf("embedding endpoint should default to main; got url=%q key=%q", c.EmbeddingBaseURL, c.EmbeddingAPIKey)
	}
}

func TestSeparateEmbeddingEndpoint(t *testing.T) {
	clearEnv(t)
	t.Setenv("OPENAI_BASE_URL", "https://router.huggingface.co/v1")
	t.Setenv("EMBEDDING_BASE_URL", "http://host.docker.internal:11434/v1")
	t.Setenv("EMBEDDING_API_KEY", "ollama")
	c := Load()
	if c.EmbeddingBaseURL != "http://host.docker.internal:11434/v1" {
		t.Errorf("embedding base url = %q, want the override", c.EmbeddingBaseURL)
	}
	if c.EmbeddingAPIKey != "ollama" {
		t.Errorf("embedding api key = %q, want override", c.EmbeddingAPIKey)
	}
}
