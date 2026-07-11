package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mealplanner/internal/config"
)

// hfServer returns an httptest server mimicking HF feature-extraction, plus the
// last request path/body it saw.
func newClientHF(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(config.Config{
		BaseURL:           "http://unused/v1",
		EmbeddingProvider: "hf",
		EmbeddingBaseURL:  srv.URL,
		EmbeddingAPIKey:   "hf_test",
		EmbeddingModel:    "nomic-ai/nomic-embed-text-v1.5",
	})
	return c, srv
}

func TestHFEmbed1D(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	c, _ := newClientHF(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[0.1, 0.2, 0.3]"))
	})
	v, err := c.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(v) != 3 || v[0] != 0.1 || v[2] != 0.3 {
		t.Fatalf("vector = %v, want [0.1 0.2 0.3]", v)
	}
	if gotPath != "/models/nomic-ai/nomic-embed-text-v1.5/pipeline/feature-extraction" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer hf_test" {
		t.Errorf("auth = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"inputs":"hello"`) {
		t.Errorf("body = %q, want inputs:hello", gotBody)
	}
}

func TestHFEmbed2DMeanPooled(t *testing.T) {
	c, _ := newClientHF(t, func(w http.ResponseWriter, r *http.Request) {
		// token matrix: mean of the two rows is [0.5, 0.5]
		w.Write([]byte("[[0.0, 1.0],[1.0, 0.0]]"))
	})
	v, err := c.Embed(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(v) != 2 || v[0] != 0.5 || v[1] != 0.5 {
		t.Fatalf("mean-pooled = %v, want [0.5 0.5]", v)
	}
}

func TestHFEmbedHTTPError(t *testing.T) {
	c, _ := newClientHF(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("Not Found"))
	})
	if _, err := c.Embed(context.Background(), "x"); err == nil {
		t.Fatal("expected error on HTTP 404")
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to mention 404", err)
	}
}

// EmbedDocument/EmbedQuery must apply their prefixes even on the HF path.
func TestHFEmbedAppliesPrefix(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte("[1.0]"))
	}))
	t.Cleanup(srv.Close)
	c := New(config.Config{
		EmbeddingProvider: "hf",
		EmbeddingBaseURL:  srv.URL,
		EmbeddingModel:    "m",
		EmbedDocPrefix:    "search_document: ",
	})
	if _, err := c.EmbedDocument(context.Background(), "lentils"); err != nil {
		t.Fatalf("EmbedDocument: %v", err)
	}
	if !strings.Contains(gotBody, `search_document: lentils`) {
		t.Errorf("body = %q, want document prefix applied", gotBody)
	}
}
