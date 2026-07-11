// Command server runs the meal-menu planner: an OpenAI-compatible, RAG-backed
// weekly menu suggester with an embedded web UI.
package main

import (
	"context"
	"io"
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

	// Re-embed menus saved under a different embedding provider/model so
	// switching profiles (local ↔ HF) keeps retrieval working on existing data.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		engine.ReindexStale(ctx)
	}()

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
	if cfg.EmbeddingProvider != "openai" || cfg.EmbeddingBaseURL != cfg.BaseURL {
		log.Printf("  embeddings: provider=%s endpoint=%s", cfg.EmbeddingProvider, cfg.EmbeddingBaseURL)
	}
	if cfg.EmbedQueryPrefix != "" || cfg.EmbedDocPrefix != "" {
		log.Printf("  embedding task prefixes: query=%q document=%q", cfg.EmbedQueryPrefix, cfg.EmbedDocPrefix)
	} else if cfg.LooksLikeNomic() {
		log.Printf("  hint: %q is a Nomic model. If retrieval quality is poor (common on Hugging Face), set", cfg.EmbeddingModel)
		log.Printf(`        EMBED_QUERY_PREFIX="search_query: " and EMBED_DOCUMENT_PREFIX="search_document: ".`)
		log.Printf("        (Skip if your serving layer already adds them — adding twice hurts.)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		diagnoseModelsFailure(ctx, client, cfg, err)
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
	// Only check the embedding model against this list when embeddings actually
	// use the same OpenAI endpoint the list came from.
	if cfg.EmbeddingProvider == "openai" && cfg.EmbeddingBaseURL == cfg.BaseURL {
		warnIfMissing(models, "EMBEDDING_MODEL", cfg.EmbeddingModel)
	}

	// Embeddings are essential for RAG and often live on a different endpoint
	// (e.g. HF serves chat but not /v1/embeddings), so verify them explicitly.
	probeEmbeddings(ctx, client, cfg)
}

// probeEmbeddings verifies the embeddings endpoint responds, with a targeted
// hint when a chat-only provider (like the HF router) is being used for them.
func probeEmbeddings(ctx context.Context, client *llm.Client, cfg config.Config) {
	vec, err := client.Embed(ctx, "ping")
	if err != nil {
		log.Printf("  embeddings probe (provider=%s model=%s @ %s) FAILED: %v",
			cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingBaseURL, err)
		switch {
		case cfg.EmbeddingProvider == "openai" && isHuggingFace(cfg.EmbeddingBaseURL):
			log.Printf("  → HF's router has no OpenAI /v1/embeddings. Set EMBEDDING_PROVIDER=hf (HF feature-extraction),")
			log.Printf("    or point embeddings at another provider (e.g. local Ollama).")
		case cfg.EmbeddingProvider == "hf":
			log.Printf("  → check EMBEDDING_MODEL is served for feature-extraction and OPENAI_API_KEY/EMBEDDING_API_KEY is a valid HF token.")
		}
		log.Printf("  → suggestions will fail until embeddings work (RAG needs them).")
		return
	}
	log.Printf("  embeddings probe (provider=%s model=%s) OK — %d dims", cfg.EmbeddingProvider, cfg.EmbeddingModel, len(vec))
}

func isHuggingFace(url string) bool {
	u := strings.ToLower(url)
	return strings.Contains(u, "huggingface") || strings.Contains(u, "hf.space") || strings.Contains(u, "hf.co")
}

// diagnoseModelsFailure turns the cryptic "/models" failure into an actionable
// message, then checks whether chat and embeddings actually work (some providers
// don't expose a usable /models list even though inference works).
func diagnoseModelsFailure(ctx context.Context, client *llm.Client, cfg config.Config, listErr error) {
	log.Printf("could not list models: %v", listErr)

	// Raw probe of <base>/models to see what the endpoint actually returned.
	status, ctype, snippet := rawProbe(ctx, cfg)
	if status != 0 {
		log.Printf("  GET %s/models → HTTP %d, content-type %q", strings.TrimRight(cfg.BaseURL, "/"), status, ctype)
		if snippet != "" {
			log.Printf("  body starts with: %s", snippet)
		}
	}
	// Targeted hints.
	if strings.Contains(listErr.Error(), "looking for beginning of value") || strings.HasPrefix(snippet, "<") {
		log.Printf("  → the endpoint returned HTML, not JSON. OPENAI_BASE_URL is probably not an OpenAI-compatible /v1 endpoint.")
	}
	if !strings.Contains(strings.TrimRight(cfg.BaseURL, "/"), "/v1") {
		log.Printf("  → OPENAI_BASE_URL=%q has no \"/v1\" path. Most OpenAI-compatible APIs need it, e.g. %s/v1",
			cfg.BaseURL, strings.TrimRight(cfg.BaseURL, "/"))
	}
	if strings.Contains(snippet, "Cannot POST") || strings.Contains(snippet, "Cannot GET") {
		log.Printf("  → the server has no such route (Express 404). The base URL host/path is wrong — it is not an inference API.")
	}
	if status == 401 || status == 403 {
		log.Printf("  → authentication failed — check OPENAI_API_KEY.")
	}
	if isHuggingFace(cfg.BaseURL) {
		log.Printf("  → Hugging Face: the router implements /chat/completions but not /models (HTML/404 here is")
		log.Printf("    usually fine — see the chat probe). Use OPENAI_BASE_URL=https://router.huggingface.co/v1")
		log.Printf("    + HF token; model ids like google/gemma-3-12b-it. It does NOT serve /v1/embeddings.")
	}

	// Functional fallback: does chat / embeddings actually respond?
	if err := client.Ping(ctx); err != nil {
		log.Printf("  chat probe (%s) FAILED: %v", cfg.ChatModel, err)
	} else {
		log.Printf("  chat probe (%s) OK", cfg.ChatModel)
	}
	probeEmbeddings(ctx, client, cfg)
	log.Printf("  (server still starts; fix the above so extraction/suggestions work)")
}

// rawProbe does a plain GET of <base>/models and returns the status, content
// type, and a short snippet of the body for diagnostics.
func rawProbe(ctx context.Context, cfg config.Config) (status int, contentType, snippet string) {
	url := strings.TrimRight(cfg.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", ""
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	s := strings.TrimSpace(string(b))
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace/newlines
	return resp.StatusCode, resp.Header.Get("Content-Type"), s
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
