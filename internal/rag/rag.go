// Package rag implements retrieval-augmented menu suggestions: it embeds and
// indexes saved menus, retrieves season-aware historical examples, and prompts
// the LLM to propose dinners (given lunches) or a full week.
package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"mealplanner/internal/jsonx"
	"mealplanner/internal/llm"
	"mealplanner/internal/menu"
	"mealplanner/internal/store"
)

// perLunchK is how many historical pairs to retrieve per provided lunch.
const perLunchK = 4

// weekK is how many historical weeks to retrieve for the full-week fallback.
const weekK = 5

// Engine ties together storage, the LLM, and ranking parameters.
type Engine struct {
	store        *store.Store
	llm          *llm.Client
	seasonWeight float64
	defaultLang  string
}

// New builds an Engine.
func New(st *store.Store, client *llm.Client, seasonWeight float64, defaultLang string) *Engine {
	return &Engine{store: st, llm: client, seasonWeight: seasonWeight, defaultLang: defaultLang}
}

// SaveMenu computes embeddings for a week and persists it. Embedding is
// best-effort: if the embeddings endpoint is unavailable, the menu is still
// saved (so no data is lost) but it won't participate in retrieval until it is
// re-saved with a working endpoint.
func (e *Engine) SaveMenu(ctx context.Context, w menu.Week) (int64, error) {
	w.Normalize()
	emb := store.Embeddings{Model: "embedding"}
	for i := range w.Days {
		lunch := w.Days[i].LunchText()
		if strings.TrimSpace(lunch) == "" {
			continue
		}
		vec, err := e.llm.Embed(ctx, lunch)
		if err != nil {
			log.Printf("warning: embedding day %d lunch failed, saving without vector: %v", i, err)
			continue
		}
		emb.Days[i] = vec
	}
	if canonical := w.CanonicalText(); canonical != "" {
		if vec, err := e.llm.Embed(ctx, canonical); err != nil {
			log.Printf("warning: embedding week failed, saving without vector: %v", err)
		} else {
			emb.Week = vec
		}
	}
	embedded := 0
	for _, v := range emb.Days {
		if len(v) > 0 {
			embedded++
		}
	}
	log.Printf("rag: indexed menu — %d day lunch vectors, week vector=%t", embedded, len(emb.Week) > 0)
	return e.store.SaveMenu(w, emb)
}

// SuggestRequest is the input for both suggestion modes.
type SuggestRequest struct {
	Mode        string   `json:"mode"`      // "dinners" | "week"
	WeekStart   string   `json:"weekStart"` // ISO date; drives seasonality
	Lunches     []string `json:"lunches"`   // up to 7 lunches (dinners mode)
	Constraints string   `json:"constraints"`
	Lang        string   `json:"lang"` // "ca" | "es"
}

// Suggest dispatches to the requested mode and returns a proposed Week.
func (e *Engine) Suggest(ctx context.Context, req SuggestRequest) (menu.Week, error) {
	lang := langName(pick(req.Lang, e.defaultLang))
	month := menu.MonthOf(req.WeekStart)
	if req.Mode == "week" || !hasAnyLunch(req.Lunches) {
		return e.suggestWeek(ctx, req, month, lang)
	}
	return e.suggestDinners(ctx, req, month, lang)
}

func (e *Engine) suggestDinners(ctx context.Context, req SuggestRequest, month int, lang string) (menu.Week, error) {
	// Retrieve season-aware historical pairs for each provided lunch.
	seen := map[string]bool{}
	var examples []store.DayPairHit
	for _, lunch := range req.Lunches {
		lunch = strings.TrimSpace(lunch)
		if lunch == "" {
			continue
		}
		qv, err := e.llm.Embed(ctx, lunch)
		if err != nil {
			return menu.Week{}, fmt.Errorf("embed query lunch: %w", err)
		}
		hits, err := e.store.RankDayPairs(qv, month, e.seasonWeight, perLunchK)
		if err != nil {
			return menu.Week{}, err
		}
		for _, h := range hits {
			key := h.LunchText + "→" + h.DinnerText
			if h.DinnerText == "" || seen[key] {
				continue
			}
			seen[key] = true
			examples = append(examples, h)
		}
	}

	log.Printf("rag: dinners mode — retrieved %d unique historical lunch→dinner pairs (target month=%d, seasonWeight=%.2f)",
		len(examples), month, e.seasonWeight)
	prompt := buildDinnersPrompt(req, month, lang, examples)
	raw, err := e.llm.ChatJSON(ctx, dinnersSystem(lang), prompt)
	if err != nil {
		return menu.Week{}, err
	}
	return parseDinners(req.Lunches, raw)
}

func (e *Engine) suggestWeek(ctx context.Context, req SuggestRequest, month int, lang string) (menu.Week, error) {
	// Query weeks by constraints text (falls back to a neutral query).
	query := strings.TrimSpace(req.Constraints)
	if query == "" {
		query = "typical weekly family menu"
	}
	qv, err := e.llm.Embed(ctx, query)
	if err != nil {
		return menu.Week{}, fmt.Errorf("embed query: %w", err)
	}
	hits, err := e.store.RankWeeks(qv, month, e.seasonWeight, weekK)
	if err != nil {
		return menu.Week{}, err
	}
	log.Printf("rag: week mode — retrieved %d past weeks (target month=%d, seasonWeight=%.2f)",
		len(hits), month, e.seasonWeight)
	prompt := buildWeekPrompt(req, month, lang, hits)
	raw, err := e.llm.ChatJSON(ctx, weekSystem(lang), prompt)
	if err != nil {
		return menu.Week{}, err
	}
	w, err := parseWeek(raw)
	if err != nil {
		return menu.Week{}, err
	}
	w.WeekStart = req.WeekStart
	return w, nil
}

// ---- Prompt building ----------------------------------------------------

func dinnersSystem(lang string) string {
	return "You are a household meal planner. You propose DINNERS for a week given the fixed " +
		"lunches, learning from the household's own past menus. Write every dish in " + lang + ". " +
		`Return ONLY JSON of the shape {"dinners":[{"name":"Mon","dish":"","notes":""}, ... 7 items Mon..Sun]}.`
}

func weekSystem(lang string) string {
	return "You are a household meal planner. You propose a FULL week of lunches and dinners, " +
		"learning from the household's own past menus. Write every dish in " + lang + ". " +
		`Return ONLY JSON of the shape {"days":[{"name":"Mon","lunch":{"dish":"","notes":""},"dinner":{"dish":"","notes":""}}, ... 7 items Mon..Sun]}.`
}

func buildDinnersPrompt(req SuggestRequest, month int, lang string, examples []store.DayPairHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Target week starts %s — season: %s (%s).\n\n", req.WeekStart, seasonName(month), monthName(month))

	b.WriteString("Fixed lunches for each day:\n")
	for i, name := range menu.DayNames {
		lunch := ""
		if i < len(req.Lunches) {
			lunch = strings.TrimSpace(req.Lunches[i])
		}
		if lunch == "" {
			lunch = "(none)"
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, lunch)
	}

	if len(examples) > 0 {
		b.WriteString("\nExamples of lunch → dinner pairings from this household's history " +
			"(reproduce this style; do not copy blindly):\n")
		for _, ex := range examples {
			fmt.Fprintf(&b, "- when lunch was \"%s\" → dinner was \"%s\"\n", ex.LunchText, ex.DinnerText)
		}
	} else {
		b.WriteString("\n(No historical pairings available yet — use good general judgement.)\n")
	}

	if c := strings.TrimSpace(req.Constraints); c != "" {
		fmt.Fprintf(&b, "\nExtra constraints from the user: %s\n", c)
	}

	b.WriteString("\nRules for the dinners you propose:\n")
	b.WriteString("1. Reproduce the household's past lunch→dinner pairing style.\n")
	b.WriteString("2. Do NOT repeat the same day's lunch dish, main protein, or cuisine.\n")
	b.WriteString("3. Keep the 7 dinners varied across the week (no repeats).\n")
	b.WriteString("4. Nutritionally complement each lunch (lighter dinner after a heavy lunch; add vegetables if lunch lacked them).\n")
	fmt.Fprintf(&b, "5. Prefer produce and dishes in season for %s.\n", seasonName(month))
	fmt.Fprintf(&b, "6. Write all dishes in %s.\n", lang)
	return b.String()
}

func buildWeekPrompt(req SuggestRequest, month int, lang string, hits []store.WeekHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Propose a full week (lunch + dinner for 7 days) starting %s — season: %s (%s).\n\n",
		req.WeekStart, seasonName(month), monthName(month))

	if len(hits) > 0 {
		b.WriteString("Examples of past weeks from this household (reproduce this style):\n")
		for _, h := range hits {
			fmt.Fprintf(&b, "\n[week of %s]\n%s\n", h.Week.WeekStart, h.Week.CanonicalText())
		}
	} else {
		b.WriteString("(No past weeks available yet — use good general judgement.)\n")
	}

	if c := strings.TrimSpace(req.Constraints); c != "" {
		fmt.Fprintf(&b, "\nExtra constraints from the user: %s\n", c)
	}

	b.WriteString("\nRules:\n")
	b.WriteString("1. Keep dishes varied across the week; don't repeat the same dish.\n")
	b.WriteString("2. Balance each day (don't pair two heavy meals; include vegetables).\n")
	fmt.Fprintf(&b, "3. Prefer produce and dishes in season for %s.\n", seasonName(month))
	fmt.Fprintf(&b, "4. Write all dishes in %s.\n", lang)
	return b.String()
}

// ---- Parsing ------------------------------------------------------------

func parseDinners(lunches []string, raw string) (menu.Week, error) {
	var out struct {
		Dinners []menu.Meal `json:"dinners"`
	}
	// Support both {"dinners":[{name,dish,notes}]} and a bare array of meals by
	// first trying a richer shape.
	type namedMeal struct {
		Name  string `json:"name"`
		Dish  string `json:"dish"`
		Notes string `json:"notes"`
	}
	var rich struct {
		Dinners []namedMeal `json:"dinners"`
	}
	cleaned := jsonx.Clean(raw)
	if err := json.Unmarshal([]byte(cleaned), &rich); err != nil || len(rich.Dinners) == 0 {
		if err2 := json.Unmarshal([]byte(cleaned), &out); err2 != nil {
			return menu.Week{}, fmt.Errorf("parse dinners: %w", err)
		}
	} else {
		out.Dinners = make([]menu.Meal, len(rich.Dinners))
		for i, d := range rich.Dinners {
			out.Dinners[i] = menu.Meal{Dish: d.Dish, Notes: d.Notes}
		}
	}

	var w menu.Week
	w.Normalize()
	for i := range w.Days {
		if i < len(lunches) {
			w.Days[i].Lunch = menu.Meal{Dish: strings.TrimSpace(lunches[i])}
		}
		if i < len(out.Dinners) {
			w.Days[i].Dinner = out.Dinners[i]
		}
	}
	return w, nil
}

func parseWeek(raw string) (menu.Week, error) {
	var parsed struct {
		Days []menu.Day `json:"days"`
	}
	if err := json.Unmarshal([]byte(jsonx.Clean(raw)), &parsed); err != nil {
		return menu.Week{}, fmt.Errorf("parse week: %w", err)
	}
	var w menu.Week
	for i := 0; i < menu.DayCount && i < len(parsed.Days); i++ {
		w.Days[i] = parsed.Days[i]
	}
	w.Normalize()
	return w, nil
}

// ---- Small helpers ------------------------------------------------------

func hasAnyLunch(lunches []string) bool {
	for _, l := range lunches {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

func pick(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func langName(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "es":
		return "Spanish"
	case "ca":
		return "Catalan"
	default:
		return "Catalan"
	}
}

func monthName(m int) string {
	names := []string{"", "January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December"}
	if m >= 1 && m <= 12 {
		return names[m]
	}
	return "unknown month"
}

func seasonName(m int) string {
	switch {
	case m == 12 || m == 1 || m == 2:
		return "winter"
	case m >= 3 && m <= 5:
		return "spring"
	case m >= 6 && m <= 8:
		return "summer"
	case m >= 9 && m <= 11:
		return "autumn"
	default:
		return "unspecified season"
	}
}
