package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits on a published export: it comes from running a third-party jar, so it is untrusted, like
// a file anyone could have written. Consumers should check what they import again.
const (
	MaxSyntaxes        = 3000
	maxIDLength        = 64
	maxNameLength      = 255
	maxLines           = 200
	maxLineLength      = 4000
	maxPatternsPerItem = 100
	maxWarnings        = 50
)

// Export is the format the exporter plugin writes (extractor/exporter, SyntaxExporter.java) and
// this repository publishes, once sanitized. Fields not listed here are dropped.
type Export struct {
	Format        string       `json:"format"`
	FormatVersion int          `json:"formatVersion"`
	Language      string       `json:"language"`
	Source        ExportSource `json:"source"`
	Events        []Item       `json:"events"`
	Conditions    []Item       `json:"conditions"`
	Effects       []Item       `json:"effects"`
	Expressions   []Item       `json:"expressions"`
	Sections      []Item       `json:"sections"`
	Structures    []Item       `json:"structures"`
	Types         []Item       `json:"types"`
	Functions     []Item       `json:"functions"`
	Warnings      []string     `json:"warnings"`
	Stats         ExportStats  `json:"stats"`
}

type ExportSource struct {
	Plugin           string `json:"plugin"`
	Version          string `json:"version"`
	SkriptVersion    string `json:"skriptVersion"`
	MinecraftVersion string `json:"minecraftVersion"`
}

type ExportStats struct {
	Total        int `json:"total"`
	Ambiguous    int `json:"ambiguous"`
	Undocumented int `json:"undocumented"`
}

type Item struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Documented      bool     `json:"documented"`
	Description     []string `json:"description"`
	Patterns        []string `json:"patterns"`
	Examples        []string `json:"examples"`
	Since           []string `json:"since"`
	RequiredPlugins []string `json:"requiredPlugins"`
	Keywords        []string `json:"keywords,omitempty"`
	Deprecated      bool     `json:"deprecated,omitempty"`
	ReturnType      string   `json:"returnType,omitempty"`
	Cancellable     bool     `json:"cancellable,omitempty"`
}

var ErrInvalidExport = errors.New("INVALID_EXPORT")

// Sanitize parses an export, rejects what is not one, and returns it cleaned and bounded, with the
// warnings about what was dropped or renamed.
func Sanitize(raw []byte) (Export, error) {
	var e Export
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, fmt.Errorf("%w: %v", ErrInvalidExport, err)
	}
	if e.Format != "skmc-syntax-export" || e.FormatVersion != 1 {
		return e, fmt.Errorf("%w: unsupported format %q v%d", ErrInvalidExport, e.Format, e.FormatVersion)
	}
	categories := []struct {
		name  string
		items *[]Item
	}{
		{"event", &e.Events}, {"condition", &e.Conditions}, {"effect", &e.Effects}, {"expression", &e.Expressions},
		{"section", &e.Sections}, {"structure", &e.Structures}, {"type", &e.Types}, {"function", &e.Functions},
	}
	total := 0
	for _, c := range categories {
		total += len(*c.items)
	}
	if total > MaxSyntaxes {
		return e, fmt.Errorf("%w: %d syntaxes, at most %d", ErrInvalidExport, total, MaxSyntaxes)
	}

	s := sanitizer{seen: map[string]string{}}
	for _, c := range categories {
		*c.items = s.items(*c.items, c.name)
	}
	e.Language = "en"
	e.Source = ExportSource{
		Plugin:           clean(e.Source.Plugin, 128),
		Version:          clean(e.Source.Version, 128),
		SkriptVersion:    clean(e.Source.SkriptVersion, 32),
		MinecraftVersion: clean(e.Source.MinecraftVersion, 32),
	}
	e.Warnings = append(lines(e.Warnings, maxWarnings), s.warnings...)
	e.Stats.Total = 0
	for _, c := range categories {
		e.Stats.Total += len(*c.items)
	}
	return e, nil
}

type sanitizer struct {
	seen     map[string]string // id → category
	warnings []string
}

func (s *sanitizer) items(items []Item, category string) []Item {
	out := []Item{}
	for _, item := range items {
		id := clean(item.ID, 1000)
		patterns := lines(item.Patterns, maxPatternsPerItem)
		if id == "" || len(patterns) == 0 {
			s.warnings = append(s.warnings, fmt.Sprintf("Dropped an element of category %s without id or pattern (%q).", category, clean(item.Name, 100)))
			continue
		}
		id = fitID(id)
		// Skript only keeps ids unique per category; an importer matches them per add-on.
		if other, taken := s.seen[id]; taken {
			renamed := fitID(id + "_" + category)
			s.warnings = append(s.warnings, fmt.Sprintf("The id %q is used in categories %s and %s: the %s one is renamed %q.", id, other, category, category, renamed))
			id = renamed
		}
		s.seen[id] = category
		name := clean(item.Name, maxNameLength)
		if name == "" {
			name = id
		}
		out = append(out, Item{
			ID:              id,
			Name:            name,
			Documented:      item.Documented,
			Description:     lines(item.Description, maxLines),
			Patterns:        patterns,
			Examples:        lines(item.Examples, maxLines),
			Since:           lines(item.Since, 20),
			RequiredPlugins: lines(item.RequiredPlugins, 20),
			Keywords:        lines(item.Keywords, 50),
			Deprecated:      item.Deprecated,
			ReturnType:      clean(item.ReturnType, 128),
			Cancellable:     item.Cancellable,
		})
	}
	return out
}

// fitID keeps an id within 64 bytes (a common database column size), unique thanks to a hash of
// the full id.
func fitID(id string) string {
	if len(id) <= maxIDLength {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	suffix := "~" + hex.EncodeToString(sum[:4])
	cut := id[:maxIDLength-len(suffix)]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + suffix
}

func lines(values []string, max int) []string {
	out := []string{}
	for _, v := range values {
		if len(out) >= max {
			break
		}
		if v = clean(v, maxLineLength); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// clean drops invalid UTF-8 and control characters (tabs and newlines kept: examples are indented
// code) and bounds the length.
func clean(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, " \n")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}
