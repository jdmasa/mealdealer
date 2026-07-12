# Meal-Menu Planner

A small, self-hosted app to plan weekly menus. It ingests the menus you already
prepare — including from a **photo or PDF** (auto-extracted into an editable form) —
and suggests new ones grounded in that history using **RAG + an LLM**.

Its primary workflow mirrors how you actually plan: **enter each day's lunch and get
proposed dinners** that reproduce your past lunch→dinner pairings, avoid repetition,
balance the day nutritionally, and respect the **season** of the target week. With no
lunches entered, it generates a full week instead.

> Ingestion currently uses **text-based PDFs** and **`.txt`/`.md`** files (no vision
> model required). Image/photo extraction exists in the backend but is disabled in the
> UI; set a multimodal `VISION_MODEL` and re-enable it later if you want photo upload.

- **Backend**: Go, single static binary, embedded frontend (`go:embed`).
- **LLM**: any **OpenAI-compatible API** (OpenAI, a local Qwen/vLLM server, etc.).
- **Storage**: embedded SQLite (pure-Go, no CGO).
- **RAG**: day-level `lunch → dinner` embeddings with brute-force, season-aware
  cosine retrieval — no external vector DB.
- **Language**: UI + suggestions in **Catalan (default)** or **Spanish** (toggle).

## Prerequisites

- **Go 1.23+** to build/run locally (`brew install go`), **or**
- **Docker** to run in a container.
- Access to an OpenAI-compatible API (base URL + key). For extraction, the chat
  model (or `VISION_MODEL`) must be **multimodal** (accept images).

## Configuration

Copy the profile template matching where you run it, then fill it in:

```bash
cp .env.local.example .env        # all-local (Ollama)
# or
cp .env.huggingface.example .env  # all-Hugging Face (set your hf_ token)
```

| Variable | Purpose | Default |
|---|---|---|
| `OPENAI_BASE_URL` | API base URL | `https://api.openai.com/v1` |
| `OPENAI_API_KEY` | API key | — (required) |
| `CHAT_MODEL` | model for suggestions | `gpt-4o-mini` |
| `EXTRACT_MODEL` | model for parsing uploaded menus/PDFs (blank = reuse `CHAT_MODEL`) | — |
| `VISION_MODEL` | image model (blank = reuse `CHAT_MODEL`) | — |
| `EMBEDDING_MODEL` | embedding model | `text-embedding-3-small` |
| `EMBEDDING_PROVIDER` | `openai` (OpenAI `/embeddings`) or `hf` (HF feature-extraction) | `openai` |
| `EMBEDDING_BASE_URL` / `EMBEDDING_API_KEY` | separate endpoint for embeddings (blank = reuse main; for `hf`, blank = HF router) | — |
| `EMBED_QUERY_PREFIX` / `EMBED_DOCUMENT_PREFIX` | Nomic task prefixes (set manually if your serving layer doesn't add them; trailing space kept) | — |
| `DB_PATH` | SQLite file path | `data/mealplanner.db` |
| `PORT` | HTTP port | `8080` |
| `OUTPUT_LANG` | default language: `ca` or `es` | `ca` |
| `SEASON_WEIGHT` | weight of seasonal proximity in ranking | `0.15` |
| `REQUEST_TIMEOUT` | HTTP read/write timeout in seconds (raise for slow local models) | `300` |

## Choosing a backend

Two ready templates are included — copy one to `.env`:

```bash
cp .env.local.example .env        # everything on local Ollama (offline, no token)
# or
cp .env.huggingface.example .env  # chat + embeddings on the Hugging Face router
```

Each profile is self-contained. The chat base URL **must include `/v1`** (e.g.
`https://router.huggingface.co/v1`) — a bare host returns HTML and fails. HF's router has no
OpenAI `/v1/embeddings`, so the HF profile sets `EMBEDDING_PROVIDER=hf` to use HF's
feature-extraction API instead; the local profile uses `EMBEDDING_PROVIDER=openai` against
Ollama. The startup log runs chat + embeddings probes and tells you exactly what's wrong if a
call fails.

## Run locally

```bash
go mod tidy          # fetch dependencies (first time)
set -a; . ./.env; set +a
go run ./cmd/server
# open http://localhost:8080
```

## Run with Docker

Local development (both compose files present — the override builds from source):

```bash
docker compose up --build
# open http://localhost:8080  (data persists in ./menudata)
```

Deployment (image published to GHCR by the GitHub Action): copy **only**
`docker-compose.yml` and your `.env` to the server — no source or build tools
needed:

```bash
docker compose pull && docker compose up -d
```

## How it works

1. **Save menus** (upload a photo/PDF or enter manually). On save, each day's lunch is
   embedded and stored as a `lunch → dinner` pair, tagged with the week's month.
2. **Propose dinners**: for each lunch you enter, the app retrieves the most similar
   past lunches — ranked by `cosine + SEASON_WEIGHT × season-proximity` — and hands the
   paired dinners to the LLM as examples, which then proposes varied, balanced,
   in-season dinners in your chosen language.
3. **Full week fallback**: with no lunches, it retrieves similar past weeks and
   generates all 14 slots.

## Notes & limitations

- Ingestion accepts **text-based PDFs** and **`.txt`/`.md`** files. **Scanned/image-only
  PDFs** have no extractable text and are not supported in this mode — retype them or
  enable image upload with a multimodal `VISION_MODEL`.
- There is **no authentication**; run it on localhost or behind your own auth/reverse
  proxy if exposed.

## Project layout

```
cmd/server/main.go     entrypoint / wiring
internal/menu          shared Week/Day/Meal model + seasonality helpers
internal/config        env configuration
internal/store         SQLite persistence + season-aware vector search
internal/llm           OpenAI-compatible client (chat, vision, embeddings)
internal/extract       photo/PDF → structured Week
internal/rag           retrieval + prompt building + suggestions
internal/api           HTTP handlers
web/                   embedded frontend (html, css, js, i18n)
```
# mealdealer
