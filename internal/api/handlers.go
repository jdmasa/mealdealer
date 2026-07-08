// Package api exposes the HTTP endpoints and serves the embedded frontend.
package api

import (
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mealplanner/internal/extract"
	"mealplanner/internal/menu"
	"mealplanner/internal/rag"
	"mealplanner/internal/store"
)

const maxUploadBytes = 20 << 20 // 20 MiB

// Server holds dependencies for the HTTP handlers.
type Server struct {
	store       *store.Store
	engine      *rag.Engine
	extractor   *extract.Extractor
	defaultLang string
}

// New builds a Server.
func New(st *store.Store, engine *rag.Engine, ex *extract.Extractor, defaultLang string) *Server {
	return &Server{store: st, engine: engine, extractor: ex, defaultLang: defaultLang}
}

// Handler wires routes and serves the embedded frontend from webFS.
func (s *Server) Handler(webFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/menus/extract", s.handleExtract)
	mux.HandleFunc("POST /api/lunches/extract", s.handleExtractLunches)
	mux.HandleFunc("POST /api/menus", s.handleCreateMenu)
	mux.HandleFunc("GET /api/menus", s.handleListMenus)
	mux.HandleFunc("GET /api/menus/{id}", s.handleGetMenu)
	mux.HandleFunc("DELETE /api/menus/{id}", s.handleDeleteMenu)
	mux.HandleFunc("POST /api/suggest", s.handleSuggest)
	mux.HandleFunc("GET /api/config", s.handleConfig)

	mux.Handle("GET /", http.FileServerFS(webFS))
	return logging(mux)
}

// statusWriter captures the response status and byte count for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// logging prints one line per request: method, path, status, size, duration.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		log.Printf("%s %s -> %d (%dB) in %s",
			r.Method, r.URL.Path, sw.status, sw.bytes, time.Since(start).Round(time.Millisecond))
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"defaultLang": s.defaultLang})
}

func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	data, filename, contentType, ok := readUpload(w, r)
	if !ok {
		return
	}
	log.Printf("extract: file=%q type=%q size=%dB", filename, contentType, len(data))

	week, err := s.extractor.Extract(r.Context(), filename, contentType, data)
	if err != nil {
		log.Printf("extract: FAILED for %q: %v", filename, err)
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	log.Printf("extract: OK for %q — %d/%d slots filled (source=%s)",
		filename, filledSlots(week), 2*menu.DayCount, week.Source)
	writeJSON(w, http.StatusOK, week)
}

func (s *Server) handleExtractLunches(w http.ResponseWriter, r *http.Request) {
	data, filename, contentType, ok := readUpload(w, r)
	if !ok {
		return
	}
	log.Printf("extract-lunches: file=%q type=%q size=%dB", filename, contentType, len(data))

	lunches, err := s.extractor.ExtractLunches(r.Context(), filename, contentType, data)
	if err != nil {
		log.Printf("extract-lunches: FAILED for %q: %v", filename, err)
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	log.Printf("extract-lunches: OK for %q — %d dated lunches", filename, len(lunches))
	writeJSON(w, http.StatusOK, map[string]any{"lunches": lunches})
}

// readUpload reads the multipart "file" field, writing an error response and
// returning ok=false on failure.
func readUpload(w http.ResponseWriter, r *http.Request) (data []byte, filename, contentType string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "could not read upload: "+err.Error())
		return nil, "", "", false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing 'file' field")
		return nil, "", "", false
	}
	defer file.Close()

	data, err = io.ReadAll(file)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read file")
		return nil, "", "", false
	}
	return data, header.Filename, header.Header.Get("Content-Type"), true
}

func (s *Server) handleCreateMenu(w http.ResponseWriter, r *http.Request) {
	var week menu.Week
	if err := decodeJSON(r, &week); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(week.WeekStart) == "" {
		writeErr(w, http.StatusBadRequest, "weekStart is required")
		return
	}
	if week.Source == "" {
		week.Source = "manual"
	}
	log.Printf("save: title=%q weekStart=%s source=%s (%d/%d slots filled)",
		week.Title, week.WeekStart, week.Source, filledSlots(week), 2*menu.DayCount)
	id, err := s.engine.SaveMenu(r.Context(), week)
	if err != nil {
		log.Printf("save: FAILED: %v", err)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("save: OK — menu id=%d", id)
	saved, err := s.store.GetMenu(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleListMenus(w http.ResponseWriter, r *http.Request) {
	menus, err := s.store.ListMenus()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if menus == nil {
		menus = []menu.Week{}
	}
	writeJSON(w, http.StatusOK, menus)
}

func (s *Server) handleGetMenu(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	week, err := s.store.GetMenu(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "menu not found")
		return
	}
	writeJSON(w, http.StatusOK, week)
}

func (s *Server) handleDeleteMenu(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.DeleteMenu(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	var req rag.SuggestRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.WeekStart) == "" {
		writeErr(w, http.StatusBadRequest, "weekStart is required")
		return
	}
	log.Printf("suggest: mode=%s weekStart=%s lang=%s lunches=%d",
		req.Mode, req.WeekStart, req.Lang, countNonEmpty(req.Lunches))
	week, err := s.engine.Suggest(r.Context(), req)
	if err != nil {
		log.Printf("suggest: FAILED: %v", err)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	week.WeekStart = req.WeekStart
	log.Printf("suggest: OK — %d/%d slots proposed", filledSlots(week), 2*menu.DayCount)
	writeJSON(w, http.StatusOK, week)
}

// filledSlots counts non-empty lunch/dinner dishes across the week.
func filledSlots(w menu.Week) int {
	n := 0
	for _, d := range w.Days {
		if strings.TrimSpace(d.Lunch.Dish) != "" {
			n++
		}
		if strings.TrimSpace(d.Dinner.Dish) != "" {
			n++
		}
	}
	return n
}

func countNonEmpty(ss []string) int {
	n := 0
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			n++
		}
	}
	return n
}

// ---- helpers ------------------------------------------------------------

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func decodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(io.LimitReader(r.Body, maxUploadBytes)).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
