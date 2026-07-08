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
	hits, err := RankOrder(st, vec, 7, 0.15, 10)
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

// RankOrder is a thin test helper around RankDayPairs.
func RankOrder(st *Store, q []float32, month int, w float64, k int) ([]DayPairHit, error) {
	return st.RankDayPairs(q, month, w, k)
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
	hits, err := st.RankDayPairs(vec, 3, 0.15, 10)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected day_pairs removed after delete, got %d", len(hits))
	}
}
