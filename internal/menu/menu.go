// Package menu defines the shared data model for weekly meal plans.
package menu

import (
	"fmt"
	"strings"
	"time"
)

// DayCount is the fixed number of days in a weekly menu.
const DayCount = 7

// DayNames are canonical day keys (Mon..Sun). The frontend localizes the
// display labels; these keys are stable and language-independent.
var DayNames = [DayCount]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// Meal is a single dish with optional notes.
type Meal struct {
	Dish  string `json:"dish"`
	Notes string `json:"notes,omitempty"`
}

// Day holds the lunch and dinner for one day of the week.
type Day struct {
	Name   string `json:"name"` // one of DayNames
	Lunch  Meal   `json:"lunch"`
	Dinner Meal   `json:"dinner"`
}

// Week is a full 7-day menu tagged with its start date (which drives
// seasonality).
type Week struct {
	ID        int64         `json:"id,omitempty"`
	Title     string        `json:"title"`
	WeekStart string        `json:"weekStart"` // ISO date (YYYY-MM-DD), required
	Days      [DayCount]Day `json:"days"`
	Source    string        `json:"source,omitempty"` // upload-image | upload-pdf | manual
	CreatedAt time.Time     `json:"createdAt,omitempty"`
}

// Month returns the 1-12 month derived from WeekStart, or 0 if unparseable.
func (w Week) Month() int {
	return MonthOf(w.WeekStart)
}

// MonthOf parses an ISO date and returns its month (1-12), or 0 on error.
func MonthOf(isoDate string) int {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(isoDate))
	if err != nil {
		return 0
	}
	return int(t.Month())
}

// CircularMonthDistance returns the distance (0-6) between two months on the
// yearly cycle, so December (12) and January (1) are distance 1. If either
// month is 0 (unknown), it returns the neutral midpoint 3.
func CircularMonthDistance(a, b int) int {
	if a < 1 || a > 12 || b < 1 || b > 12 {
		return 3
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	if d > 6 {
		d = 12 - d
	}
	return d
}

// LunchText returns a canonical text representation of a day's lunch, used for
// embedding and retrieval.
func (d Day) LunchText() string {
	return mealText(d.Lunch)
}

// DinnerText returns a canonical text representation of a day's dinner.
func (d Day) DinnerText() string {
	return mealText(d.Dinner)
}

func mealText(m Meal) string {
	s := strings.TrimSpace(m.Dish)
	if n := strings.TrimSpace(m.Notes); n != "" {
		s += " (" + n + ")"
	}
	return s
}

// CanonicalText builds a single text blob summarizing the whole week, used to
// embed the week for the full-week fallback retrieval.
func (w Week) CanonicalText() string {
	var b strings.Builder
	for i := range w.Days {
		d := w.Days[i]
		lunch := d.LunchText()
		dinner := d.DinnerText()
		if lunch == "" && dinner == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: lunch=%s | dinner=%s\n", d.Name, lunch, dinner)
	}
	return strings.TrimSpace(b.String())
}

// Normalize ensures the Days array has canonical day names in order. Any dish
// data already present is preserved; only the Name field is enforced.
func (w *Week) Normalize() {
	for i := range w.Days {
		w.Days[i].Name = DayNames[i]
	}
}
