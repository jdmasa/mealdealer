package store

import (
	"path/filepath"
	"testing"

	"mealplanner/internal/menu"
)

// mkWeek builds a week whose Monday has the given lunch and dinner (a same-day
// lunch->dinner pair, which is what retrieval indexes).
func mkWeek(title, weekStart, monLunch, monDinner string) menu.Week {
	w := menu.Week{Title: title, WeekStart: weekStart}
	w.Normalize()
	w.Days[0].Lunch = menu.Meal{Dish: monLunch}
	w.Days[0].Dinner = menu.Meal{Dish: monDinner}
	return w
}

// TestSaveAndRetrieve verifies persistence, round-trip, and that the
// season-aware ranking prefers the same-season candidate when cosine ties.
func TestSaveAndRetrieve(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Two weeks with identical lunch vectors but different months. The July
	// week should rank above the January week when the target month is July.
	summer := mkWeek("summer", "2026-07-06", "Amanida", "Gaspatxo")
	winter := mkWeek("winter", "2026-01-05", "Amanida", "Escudella")

	vec := []float32{1, 0, 0}
	summerEmb := Embeddings{Model: "m", Week: vec}
	summerEmb.Days[0] = vec
	winterEmb := Embeddings{Model: "m", Week: vec}
	winterEmb.Days[0] = vec

	if _, err := st.SaveMenu(summer, summerEmb); err != nil {
		t.Fatalf("save summer: %v", err)
	}
	if _, err := st.SaveMenu(winter, winterEmb); err != nil {
		t.Fatalf("save winter: %v", err)
	}

	menus, err := st.ListMenus()
	if err != nil || len(menus) != 2 {
		t.Fatalf("list: got %d menus, err=%v", len(menus), err)
	}

	// Target month July (7): summer (month 7) must outrank winter (month 1).
	hits, err := st.RankDayPairs(vec, "m", 7, 0.15, 10)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected >=2 day pairs, got %d", len(hits))
	}
	if hits[0].DinnerText != "Gaspatxo" {
		t.Errorf("expected summer dinner (Gaspatxo) ranked first, got %q (scores: %.3f vs %.3f)",
			hits[0].DinnerText, hits[0].Score, hits[1].Score)
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("expected summer score > winter score, got %.4f <= %.4f", hits[0].Score, hits[1].Score)
	}
}

// TestModelFilterAndReindex verifies that retrieval only sees vectors from the
// requested embedding model, and that MenusNeedingReindex flags menus stored
// under a different model.
func TestModelFilterAndReindex(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	vec := []float32{1, 0, 0}
	oldEmb := Embeddings{Model: "openai/nomic-embed-text", Week: vec}
	oldEmb.Days[0] = vec
	oldID, err := st.SaveMenu(mkWeek("old", "2026-02-02", "Sopa", "Truita"), oldEmb)
	if err != nil {
		t.Fatalf("save old: %v", err)
	}

	newEmb := Embeddings{Model: "hf/intfloat/multilingual-e5-base", Week: vec}
	newEmb.Days[0] = vec
	if _, err := st.SaveMenu(mkWeek("new", "2026-02-09", "Arros", "Peix"), newEmb); err != nil {
		t.Fatalf("save new: %v", err)
	}

	// Retrieval under the new model must not see the old-model vector.
	hits, err := st.RankDayPairs(vec, "hf/intfloat/multilingual-e5-base", 2, 0.15, 10)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if len(hits) != 1 || hits[0].DinnerText != "Peix" {
		t.Fatalf("expected only the new-model pair (Peix), got %+v", hits)
	}

	// The old menu must be flagged for reindexing under the new model.
	ids, err := st.MenusNeedingReindex("hf/intfloat/multilingual-e5-base")
	if err != nil {
		t.Fatalf("needs-reindex: %v", err)
	}
	if len(ids) != 1 || ids[0] != oldID {
		t.Fatalf("expected [%d] to need reindex, got %v", oldID, ids)
	}

	// Re-save the old menu under the new model (what ReindexStale does) — it
	// should then participate in retrieval and drop off the stale list.
	w, err := st.GetMenu(oldID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	reEmb := Embeddings{Model: "hf/intfloat/multilingual-e5-base", Week: vec}
	reEmb.Days[0] = vec
	if _, err := st.SaveMenu(w, reEmb); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	if ids, _ := st.MenusNeedingReindex("hf/intfloat/multilingual-e5-base"); len(ids) != 0 {
		t.Fatalf("expected no stale menus after reindex, got %v", ids)
	}
	if hits, _ := st.RankDayPairs(vec, "hf/intfloat/multilingual-e5-base", 2, 0.15, 10); len(hits) != 2 {
		t.Fatalf("expected both pairs retrievable after reindex, got %d", len(hits))
	}
}

func TestDeleteCascades(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	vec := []float32{0, 1, 0}
	emb := Embeddings{Model: "m", Week: vec}
	emb.Days[0] = vec
	id, err := st.SaveMenu(mkWeek("x", "2026-03-02", "Sopa", "Truita"), emb)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := st.DeleteMenu(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	hits, err := st.RankDayPairs(vec, "m", 3, 0.15, 10)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected day_pairs removed after delete, got %d", len(hits))
	}
}
