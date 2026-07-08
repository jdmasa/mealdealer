// Command server runs the meal-menu planner: an OpenAI-compatible, RAG-backed
// weekly menu suggester with an embedded web UI.
package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"mealplanner/internal/api"
	"mealplanner/internal/config"
	"mealplanner/internal/extract"
	"mealplanner/internal/llm"
	"mealplanner/internal/rag"
	"mealplanner/internal/store"
	"mealplanner/web"
)

func main() {
	cfg := config.Load()
	if cfg.APIKey == "" {
		log.Println("warning: OPENAI_API_KEY is empty — extraction and suggestions will fail until it is set")
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	client := llm.New(cfg)
	checkLLMConnection(client, cfg)

	engine := rag.New(st, client, cfg.SeasonWeight, cfg.OutputLang)
	extractor := extract.New(client)
	srv := api.New(st, engine, extractor, cfg.OutputLang)

	handler := srv.Handler(web.Files)

	// Extraction/suggestion calls hit an external LLM and local models can be
	// slow, so read/write timeouts are generous and configurable.
	reqTimeout := time.Duration(cfg.RequestTimeout) * time.Second
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      reqTimeout,
		ReadTimeout:       reqTimeout,
	}

	log.Printf("meal-menu planner listening on http://localhost:%s (db=%s, chat=%s, extract=%s, lang=%s)",
		cfg.Port, cfg.DBPath, cfg.ChatModel, cfg.ExtractModel, cfg.OutputLang)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

// checkLLMConnection probes the configured OpenAI-compatible endpoint at startup
// and prints a clear status line: the available models on success, or the error
// on failure. It never aborts — the server still starts so the UI is reachable.
func checkLLMConnection(client *llm.Client, cfg config.Config) {
	log.Printf("checking LLM connection: %s (chat=%s, extract=%s, embeddings=%s)…",
		cfg.BaseURL, cfg.ChatModel, cfg.ExtractModel, cfg.EmbeddingModel)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		log.Printf("LLM connection FAILED: %v", err)
		log.Printf("  → check OPENAI_BASE_URL / OPENAI_API_KEY; suggestions and extraction will not work until this succeeds")
		return
	}

	log.Printf("LLM connection OK — %d model(s) available", len(models))
	if len(models) > 0 {
		shown := models
		const max = 25
		truncated := false
		if len(shown) > max {
			shown = shown[:max]
			truncated = true
		}
		list := strings.Join(shown, ", ")
		if truncated {
			list += ", …"
		}
		log.Printf("  models: %s", list)
	}

	// Warn if the configured models aren't in the advertised list (some
	// providers still accept them, so this is informational only).
	warnIfMissing(models, "CHAT_MODEL", cfg.ChatModel)
	warnIfMissing(models, "EXTRACT_MODEL", cfg.ExtractModel)
	warnIfMissing(models, "EMBEDDING_MODEL", cfg.EmbeddingModel)
}

func warnIfMissing(models []string, label, want string) {
	if want == "" || len(models) == 0 {
		return
	}
	// Compare ignoring an optional ":tag" suffix (e.g. Ollama's ":latest"), so
	// "llama3.2" matches "llama3.2:latest".
	base := func(s string) string {
		if i := strings.IndexByte(s, ':'); i >= 0 {
			return s[:i]
		}
		return s
	}
	wantBase := base(want)
	for _, m := range models {
		if m == want || base(m) == wantBase {
			return
		}
	}
	log.Printf("  note: %s=%q was not in the advertised model list (it may still work)", label, want)
}
