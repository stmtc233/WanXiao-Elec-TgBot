// Package schools provides the list of WanXiao school codes (customer codes) and searching over it.
package schools

import (
	_ "embed"
	"encoding/csv"
	"log"
	"sort"
	"strings"
	"sync"
	"unicode"
)

//go:embed schools.csv
var schoolsCSV string

type School struct {
	Code string
	Name string
}

var (
	loadOnce sync.Once
	all      []School
	byCode   map[string]School
)

func load() {
	loadOnce.Do(func() {
		records, err := csv.NewReader(strings.NewReader(schoolsCSV)).ReadAll()
		if err != nil {
			log.Printf("[Schools] parse schools.csv failed: %v", err)
		}
		byCode = make(map[string]School, len(records))
		for i, r := range records {
			if i == 0 || len(r) < 2 { // header
				continue
			}
			s := School{Code: strings.TrimSpace(r[0]), Name: strings.TrimSpace(r[1])}
			if s.Code == "" || s.Name == "" {
				continue
			}
			all = append(all, s)
			byCode[s.Code] = s
		}
	})
}

// Count returns the number of known schools.
func Count() int {
	load()
	return len(all)
}

// Lookup finds a school by its code.
func Lookup(code string) (School, bool) {
	load()
	s, ok := byCode[strings.TrimSpace(code)]
	return s, ok
}

// IsCode reports whether s looks like a school code (digits only).
func IsCode(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalize(s string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s))
}

// Search returns schools whose name contains every keyword (or whose code matches),
// best matches first, and the total number of matches.
func Search(query string, limit int) ([]School, int) {
	load()
	keywords := strings.Fields(strings.ToLower(query))
	if len(keywords) == 0 {
		return nil, 0
	}
	whole := normalize(query)

	type scored struct {
		School
		score int
	}
	var matches []scored
	for _, s := range all {
		name := normalize(s.Name)
		score := -1
		switch {
		case s.Code == whole:
			score = 0
		case name == whole:
			score = 1
		case strings.HasPrefix(name, whole):
			score = 2
		case strings.Contains(name, whole):
			score = 3
		case containsAll(name, keywords):
			score = 4
		case IsCode(whole) && strings.HasPrefix(s.Code, whole):
			score = 5
		}
		if score >= 0 {
			matches = append(matches, scored{s, score})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return len([]rune(matches[i].Name)) < len([]rune(matches[j].Name))
	})

	total := len(matches)
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	result := make([]School, len(matches))
	for i, m := range matches {
		result[i] = m.School
	}
	return result, total
}

func containsAll(name string, keywords []string) bool {
	for _, k := range keywords {
		if !strings.Contains(name, k) {
			return false
		}
	}
	return true
}
