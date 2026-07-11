// Package extract turns an uploaded photo or PDF of a menu into a structured
// (partial) Week, ready to prefill the edit form.
package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ledongthuc/pdf"

	"mealplanner/internal/jsonx"
	"mealplanner/internal/llm"
	"mealplanner/internal/menu"
)

// Extractor extracts menus from uploaded files.
type Extractor struct {
	llm *llm.Client
}

// New builds an Extractor.
func New(client *llm.Client) *Extractor {
	return &Extractor{llm: client}
}

// extractedWeek is the loose JSON shape we ask the model to return.
type extractedWeek struct {
	Title     string     `json:"title"`
	WeekStart string     `json:"weekStart"`
	Days      []menu.Day `json:"days"`
}

const schemaHint = `Return ONLY a JSON object of this exact shape:
{
  "title": "short label for the week, or empty",
  "weekStart": "the week's Monday as YYYY-MM-DD if visible, else empty",
  "days": [
    {"name":"Mon","lunch":{"dish":"","notes":""},"dinner":{"dish":"","notes":""}},
    {"name":"Tue", ...}, {"name":"Wed", ...}, {"name":"Thu", ...},
    {"name":"Fri", ...}, {"name":"Sat", ...}, {"name":"Sun", ...}
  ]
}
Always include exactly 7 day objects in Mon..Sun order. Use empty strings for
anything you cannot read. Transcribe every dish in its ORIGINAL language exactly
as written — do NOT translate. Put side dishes or clarifications in "notes".`

const extractSystem = "You extract weekly meal menus from images or text into structured JSON. " + schemaHint

// Extract dispatches on content type: images go to the vision model, PDFs are
// text-extracted then structured by the chat model.
func (e *Extractor) Extract(ctx context.Context, filename, contentType string, data []byte) (menu.Week, error) {
	kind := detectKind(filename, contentType, data)
	switch kind {
	case "image":
		raw, err := e.llm.VisionJSON(ctx, extractSystem,
			"Extract the weekly menu from this image.", data, contentType)
		if err != nil {
			return menu.Week{}, err
		}
		w, err := parseExtracted(raw)
		w.Source = "upload-image"
		return w, err
	case "pdf":
		text, err := pdfText(data)
		if err != nil {
			return menu.Week{}, fmt.Errorf("read pdf: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return menu.Week{}, fmt.Errorf("no text found in PDF (it may be a scan — upload a text PDF or a .txt file)")
		}
		w, err := e.structureText(ctx, text)
		w.Source = "upload-pdf"
		return w, err
	case "text":
		if strings.TrimSpace(string(data)) == "" {
			return menu.Week{}, fmt.Errorf("the uploaded text file is empty")
		}
		w, err := e.structureText(ctx, string(data))
		w.Source = "upload-text"
		return w, err
	default:
		return menu.Week{}, fmt.Errorf("unsupported file type %q (upload a text PDF, .txt, or .md file)", contentType)
	}
}

func (e *Extractor) structureText(ctx context.Context, text string) (menu.Week, error) {
	raw, err := e.llm.ExtractJSON(ctx, extractSystem,
		"Extract the weekly menu from this text:\n\n"+text)
	if err != nil {
		return menu.Week{}, err
	}
	return parseExtracted(raw)
}

// LunchItem is one lunch tagged with the day-of-month NUMBER printed for it in
// the menu (1-31). The app maps these numbers onto whichever week the user picks
// — so the model never has to find a week or compute a date.
type LunchItem struct {
	Day   int    `json:"day"`
	Dish  string `json:"dish"`
	Notes string `json:"notes,omitempty"`
}

// lunchFormatRules is the shared instruction: read every workday lunch in the
// whole document, each tagged with its printed day-of-month number. This is the
// simplest possible task for the model (no week logic, no date arithmetic),
// which makes it robust across models — the app does week selection in code.
const lunchFormatRules = `Extract EVERY lunch printed anywhere in this menu (all weeks), reading top to bottom.

Return ONLY a JSON object of this exact shape:
{"lunches":[{"day":<number>,"dish":"","notes":""}, ...]}

RULES:
- "day" is the day-of-MONTH NUMBER printed next to that day (e.g. 8, 17, 23) —
  NOT a position/index and NOT a date. Read it from the document.
- The menu is a TABLE: a row of day-of-month numbers (e.g. "1 2 3 4 5") is
  followed by that week's lunches in the SAME left-to-right order. Pair the 1st
  lunch with the 1st number, the 2nd lunch with the 2nd number, and so on; the
  next row of numbers begins the next week. Count carefully — do not shift.
- Extract ONLY lunches. In Catalan/Spanish canteen menus the lunch is the
  "dinar"/"comida" block. COMPLETELY IGNORE the "sopar"/"cena" (dinner) block.
- One entry per printed day; never merge days; never invent a day that is not
  printed. Include every week/day you can find (a month usually has ~20).
- In "dish", list that day's courses in printed order (starter, main, dessert)
  separated by " · ". Transcribe in the ORIGINAL language; do NOT translate.`

const lunchSystem = "You extract ONLY lunches (never dinners) from meal menus into structured JSON. " + lunchFormatRules

// ExtractLunches reads every workday lunch from an uploaded menu, each tagged
// with its printed day-of-month number. Week selection happens later, in code.
func (e *Extractor) ExtractLunches(ctx context.Context, filename, contentType string, data []byte) ([]LunchItem, error) {
	kind := detectKind(filename, contentType, data)
	const instruction = "Menu content:\n\n"
	var raw string
	var err error
	switch kind {
	case "image":
		raw, err = e.llm.VisionJSON(ctx, lunchSystem, "Extract every lunch from this menu image.", data, contentType)
	case "pdf":
		text, perr := pdfText(data)
		if perr != nil {
			return nil, fmt.Errorf("read pdf: %w", perr)
		}
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("no text found in PDF (it may be a scan — upload a text PDF or a .txt file)")
		}
		raw, err = e.llm.ExtractJSON(ctx, lunchSystem, instruction+text)
	case "text":
		if strings.TrimSpace(string(data)) == "" {
			return nil, fmt.Errorf("the uploaded text file is empty")
		}
		raw, err = e.llm.ExtractJSON(ctx, lunchSystem, instruction+string(data))
	default:
		return nil, fmt.Errorf("unsupported file type %q (upload a text PDF, .txt, or .md file)", contentType)
	}
	if err != nil {
		return nil, err
	}
	return parseLunches(raw)
}

func parseLunches(raw string) ([]LunchItem, error) {
	// Accept "day" as either a JSON number or a numeric string.
	var parsed struct {
		Lunches []struct {
			Day   json.Number `json:"day"`
			Dish  string      `json:"dish"`
			Notes string      `json:"notes"`
		} `json:"lunches"`
	}
	if err := json.Unmarshal([]byte(jsonx.Clean(raw)), &parsed); err != nil {
		return nil, fmt.Errorf("parse lunches: %w", err)
	}
	out := make([]LunchItem, 0, len(parsed.Lunches))
	for _, l := range parsed.Lunches {
		day, _ := l.Day.Int64()
		if day < 1 || day > 31 || strings.TrimSpace(l.Dish) == "" {
			continue
		}
		out = append(out, LunchItem{Day: int(day), Dish: l.Dish, Notes: l.Notes})
	}
	return out, nil
}

func detectKind(filename, contentType string, data []byte) string {
	ct := strings.ToLower(contentType)
	name := strings.ToLower(filename)
	if strings.HasPrefix(ct, "image/") ||
		strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") ||
		strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".webp") ||
		strings.HasSuffix(name, ".gif") {
		return "image"
	}
	if ct == "application/pdf" || strings.HasSuffix(name, ".pdf") {
		return "pdf"
	}
	if strings.HasPrefix(ct, "text/") ||
		strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".md") ||
		strings.HasSuffix(name, ".text") {
		return "text"
	}
	// Fall back to content sniffing.
	sniff := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(sniff, "image/"):
		return "image"
	case sniff == "application/pdf":
		return "pdf"
	case strings.HasPrefix(sniff, "text/"):
		return "text"
	}
	return ""
}

func pdfText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	rd, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rd); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func parseExtracted(raw string) (menu.Week, error) {
	var ew extractedWeek
	if err := json.Unmarshal([]byte(jsonx.Clean(raw)), &ew); err != nil {
		return menu.Week{}, fmt.Errorf("parse extracted menu: %w", err)
	}
	w := menu.Week{Title: ew.Title, WeekStart: ew.WeekStart}
	for i := 0; i < menu.DayCount && i < len(ew.Days); i++ {
		w.Days[i] = ew.Days[i]
	}
	w.Normalize()
	return w, nil
}
