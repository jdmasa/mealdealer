// Package jsonx helps recover a JSON object from LLM text output that may be
// wrapped in markdown code fences or surrounded by prose.
package jsonx

import "strings"

// Clean extracts the most likely JSON object from a model's text response. It
// strips ```json fences and trims anything outside the outermost braces.
func Clean(s string) string {
	s = strings.TrimSpace(s)

	// Remove surrounding code fences if present.
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}

	// Narrow to the outermost { ... }.
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
