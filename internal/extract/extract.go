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

// DatedLunch is a single lunch tied to a calendar date, extracted from a weekly
// or monthly lunch menu.
type DatedLunch struct {
	Date  string `json:"date"` // ISO YYYY-MM-DD (best effort; may be empty)
	Dish  string `json:"dish"`
	Notes string `json:"notes,omitempty"`
}

const lunchSchemaHint = `Return ONLY a JSON object of this exact shape:
{"lunches":[{"date":"YYYY-MM-DD","dish":"","notes":""}, ...]}

STRICT RULES:
- Extract ONLY lunches. In Catalan/Spanish canteen menus the lunch is the
  "dinar"/"comida" block. COMPLETELY IGNORE the "sopar"/"cena" (dinner) block —
  never copy any dinner text into the output.
- Return EXACTLY ONE entry per dated day the document covers. Never merge two
  days into one entry. Never invent days that are not printed.
- Infer each full ISO date (YYYY-MM-DD) from the day number and the month+year
  named in the document (e.g. days 1-5 under "juny 2026" → 2026-06-01 … 2026-06-05).
- In "dish", list that day's lunch courses in printed order (starter, main,
  dessert) separated by " · ", e.g. "Amanida · Estofat de gall dindi · Fruita".
- Transcribe in the ORIGINAL language; do NOT translate.
- Omit only days that genuinely have no lunch printed.`

const lunchSystem = "You extract ONLY lunches (never dinners) from weekly or monthly meal menus into structured JSON. " + lunchSchemaHint

// ExtractLunches pulls the lunches (ignoring dinners) from an uploaded weekly or
// monthly menu, each tied to its calendar date.
func (e *Extractor) ExtractLunches(ctx context.Context, filename, contentType string, data []byte) ([]DatedLunch, error) {
	kind := detectKind(filename, contentType, data)
	var raw string
	var err error
	switch kind {
	case "image":
		raw, err = e.llm.VisionJSON(ctx, lunchSystem, "Extract only the lunches from this menu image.", data, contentType)
	case "pdf":
		text, perr := pdfText(data)
		if perr != nil {
			return nil, fmt.Errorf("read pdf: %w", perr)
		}
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("no text found in PDF (it may be a scan — upload a text PDF or a .txt file)")
		}
		raw, err = e.llm.ExtractJSON(ctx, lunchSystem, "Extract only the lunches from this menu text:\n\n"+text)
	case "text":
		if strings.TrimSpace(string(data)) == "" {
			return nil, fmt.Errorf("the uploaded text file is empty")
		}
		raw, err = e.llm.ExtractJSON(ctx, lunchSystem, "Extract only the lunches from this menu text:\n\n"+string(data))
	default:
		return nil, fmt.Errorf("unsupported file type %q (upload a text PDF, .txt, or .md file)", contentType)
	}
	if err != nil {
		return nil, err
	}
	return parseLunches(raw)
}

func parseLunches(raw string) ([]DatedLunch, error) {
	var parsed struct {
		Lunches []DatedLunch `json:"lunches"`
	}
	if err := json.Unmarshal([]byte(jsonx.Clean(raw)), &parsed); err != nil {
		return nil, fmt.Errorf("parse lunches: %w", err)
	}
	return parsed.Lunches, nil
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
