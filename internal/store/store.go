// Package store persists menus and their embeddings in SQLite and performs
// brute-force, season-aware vector retrieval.
package store

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"

	"mealplanner/internal/menu"
)

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Embeddings bundles the vectors computed for a menu at save time.
type Embeddings struct {
	Model string
	Days  [menu.DayCount][]float32 // per-day lunch embeddings
	Week  []float32                // whole-week embedding
}

// DayPairHit is a retrieved historical lunch->dinner pairing with its score.
type DayPairHit struct {
	LunchText  string
	DinnerText string
	Month      int
	Score      float64
}

// WeekHit is a retrieved historical week with its score.
type WeekHit struct {
	Week  menu.Week
	Month int
	Score float64
}

// Open opens (creating if needed) the SQLite database at path and runs
// migrations.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite is a single file; a single connection avoids "database is locked".
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS menus (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	title      TEXT NOT NULL,
	week_start TEXT NOT NULL,
	source     TEXT,
	data_json  TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS day_pairs (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	menu_id         INTEGER NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
	day_index       INTEGER NOT NULL,
	month           INTEGER NOT NULL,
	lunch_text      TEXT NOT NULL,
	dinner_text     TEXT NOT NULL,
	lunch_embedding BLOB NOT NULL,
	model           TEXT NOT NULL,
	dims            INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_day_pairs_menu ON day_pairs(menu_id);
CREATE TABLE IF NOT EXISTS week_vectors (
	menu_id   INTEGER PRIMARY KEY REFERENCES menus(id) ON DELETE CASCADE,
	month     INTEGER NOT NULL,
	embedding BLOB NOT NULL,
	model     TEXT NOT NULL,
	dims      INTEGER NOT NULL
);
`
	_, err := s.db.Exec(schema)
	return err
}

// SaveMenu inserts (or replaces, when w.ID > 0) a menu together with its
// embeddings. It returns the stored menu ID.
func (s *Store) SaveMenu(w menu.Week, emb Embeddings) (int64, error) {
	w.Normalize()
	month := w.Month()
	data, err := json.Marshal(w.Days)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	id := w.ID
	if id > 0 {
		// Update in place and clear derived rows so they can be rebuilt.
		if _, err := tx.Exec(
			`UPDATE menus SET title=?, week_start=?, source=?, data_json=? WHERE id=?`,
			w.Title, w.WeekStart, w.Source, string(data), id,
		); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM day_pairs WHERE menu_id=?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM week_vectors WHERE menu_id=?`, id); err != nil {
			return 0, err
		}
	} else {
		res, err := tx.Exec(
			`INSERT INTO menus (title, week_start, source, data_json, created_at) VALUES (?,?,?,?,?)`,
			w.Title, w.WeekStart, w.Source, string(data), time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	}

	for i := range w.Days {
		vec := emb.Days[i]
		if len(vec) == 0 {
			continue // skip empty lunches (nothing to embed)
		}
		if _, err := tx.Exec(
			`INSERT INTO day_pairs (menu_id, day_index, month, lunch_text, dinner_text, lunch_embedding, model, dims)
			 VALUES (?,?,?,?,?,?,?,?)`,
			id, i, month, w.Days[i].LunchText(), w.Days[i].DinnerText(),
			packFloats(vec), emb.Model, len(vec),
		); err != nil {
			return 0, err
		}
	}

	if len(emb.Week) > 0 {
		if _, err := tx.Exec(
			`INSERT INTO week_vectors (menu_id, month, embedding, model, dims) VALUES (?,?,?,?,?)`,
			id, month, packFloats(emb.Week), emb.Model, len(emb.Week),
		); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// ListMenus returns all stored menus (newest first) without embeddings.
func (s *Store) ListMenus() ([]menu.Week, error) {
	rows, err := s.db.Query(
		`SELECT id, title, week_start, source, data_json, created_at FROM menus ORDER BY week_start DESC, id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []menu.Week
	for rows.Next() {
		w, err := scanMenu(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetMenu returns a single menu by ID.
func (s *Store) GetMenu(id int64) (menu.Week, error) {
	row := s.db.QueryRow(
		`SELECT id, title, week_start, source, data_json, created_at FROM menus WHERE id=?`, id,
	)
	return scanMenu(row)
}

// DeleteMenu removes a menu and its derived rows.
func (s *Store) DeleteMenu(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM day_pairs WHERE menu_id=?`,
		`DELETE FROM week_vectors WHERE menu_id=?`,
		`DELETE FROM menus WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// scanner abstracts *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanMenu(sc scanner) (menu.Week, error) {
	var (
		w        menu.Week
		dataJSON string
		created  string
		source   sql.NullString
	)
	if err := sc.Scan(&w.ID, &w.Title, &w.WeekStart, &source, &dataJSON, &created); err != nil {
		return menu.Week{}, err
	}
	w.Source = source.String
	if t, err := time.Parse(time.RFC3339, created); err == nil {
		w.CreatedAt = t
	}
	if err := json.Unmarshal([]byte(dataJSON), &w.Days); err != nil {
		return menu.Week{}, err
	}
	w.Normalize()
	return w, nil
}

// RankDayPairs returns the top-K historical lunch->dinner pairs ranked by a
// season-aware score combining cosine similarity of the lunch with proximity of
// the target month.
func (s *Store) RankDayPairs(query []float32, targetMonth int, seasonWeight float64, topK int) ([]DayPairHit, error) {
	rows, err := s.db.Query(`SELECT month, lunch_text, dinner_text, lunch_embedding FROM day_pairs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []DayPairHit
	for rows.Next() {
		var (
			month         int
			lunch, dinner string
			blob          []byte
		)
		if err := rows.Scan(&month, &lunch, &dinner, &blob); err != nil {
			return nil, err
		}
		vec := unpackFloats(blob)
		score := seasonAwareScore(query, vec, targetMonth, month, seasonWeight)
		hits = append(hits, DayPairHit{LunchText: lunch, DinnerText: dinner, Month: month, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return trimDayPairs(hits, topK), nil
}

// RankWeeks returns the top-K historical weeks ranked by the season-aware score.
func (s *Store) RankWeeks(query []float32, targetMonth int, seasonWeight float64, topK int) ([]WeekHit, error) {
	rows, err := s.db.Query(
		`SELECT m.id, m.title, m.week_start, m.source, m.data_json, m.created_at, wv.month, wv.embedding
		 FROM week_vectors wv JOIN menus m ON m.id = wv.menu_id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []WeekHit
	for rows.Next() {
		var (
			w        menu.Week
			dataJSON string
			created  string
			source   sql.NullString
			month    int
			blob     []byte
		)
		if err := rows.Scan(&w.ID, &w.Title, &w.WeekStart, &source, &dataJSON, &created, &month, &blob); err != nil {
			return nil, err
		}
		w.Source = source.String
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			w.CreatedAt = t
		}
		if err := json.Unmarshal([]byte(dataJSON), &w.Days); err != nil {
			return nil, err
		}
		w.Normalize()
		score := seasonAwareScore(query, unpackFloats(blob), targetMonth, month, seasonWeight)
		hits = append(hits, WeekHit{Week: w, Month: month, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

func trimDayPairs(hits []DayPairHit, topK int) []DayPairHit {
	if topK > 0 && len(hits) > topK {
		return hits[:topK]
	}
	return hits
}

// seasonAwareScore blends cosine similarity with seasonal proximity:
//
//	score = cosine + seasonWeight * (1 - circularMonthDistance/6)
func seasonAwareScore(a, b []float32, targetMonth, candMonth int, seasonWeight float64) float64 {
	cos := cosine(a, b)
	dist := menu.CircularMonthDistance(targetMonth, candMonth)
	seasonProximity := 1.0 - float64(dist)/6.0
	return cos + seasonWeight*seasonProximity
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func packFloats(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func unpackFloats(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}
