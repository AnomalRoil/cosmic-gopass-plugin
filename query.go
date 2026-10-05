package main

import (
	"cmp"
	"context"
	"path"
	"slices"
	"strings"
)

// mode selects what gets copied when an entry is activated.
type mode int

const (
	modePassword mode = iota
	modeKey           // a key of the secret, e.g. user or url
	modeOTP
	modeName // the last path component of the entry
)

type query struct {
	mode  mode
	key   string   // set when mode is modeKey
	terms []string // lower-cased, all must match
}

// parseQuery parses "gp [-o|-otp] [-n|-name] [:KEY] TERM...".
// Options may appear anywhere; when several are given, the last one wins.
func parseQuery(s string) query {
	s, _ = strings.CutPrefix(s, "gp ")
	var q query
	for _, word := range strings.Fields(strings.ToLower(s)) {
		switch {
		case word == "-o" || word == "-otp":
			q.mode, q.key = modeOTP, ""
		case word == "-n" || word == "-name":
			q.mode, q.key = modeName, ""
		case len(word) > 1 && word[0] == ':':
			q.mode, q.key = modeKey, word[1:]
		default:
			q.terms = append(q.terms, word)
		}
	}
	return q
}

type entry struct {
	name  string
	lower string
}

func newEntries(names []string) []entry {
	entries := make([]entry, 0, len(names))
	for _, name := range names {
		if name != "" {
			entries = append(entries, entry{name: name, lower: strings.ToLower(name)})
		}
	}
	return entries
}

// search returns the names of up to limit entries containing every term, best first:
// exact matches, then entries with the most terms in their last path component,
// then shorter names.
func search(ctx context.Context, entries []entry, terms []string, limit int) ([]string, error) {
	type match struct {
		name     string
		exact    bool
		baseHits int
	}
	full := strings.Join(terms, " ")
	var matches []match
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m := match{name: e.name, exact: e.lower == full}
		base := path.Base(e.lower)
		ok := true
		for _, t := range terms {
			if !strings.Contains(e.lower, t) {
				ok = false
				break
			}
			if strings.Contains(base, t) {
				m.baseHits++
			}
		}
		if ok {
			matches = append(matches, m)
		}
	}
	slices.SortFunc(matches, func(a, b match) int {
		if a.exact != b.exact {
			if a.exact {
				return -1
			}
			return 1
		}
		return cmp.Or(
			cmp.Compare(b.baseHits, a.baseHits),
			cmp.Compare(len(a.name), len(b.name)),
			strings.Compare(a.name, b.name),
		)
	})
	names := make([]string, 0, min(limit, len(matches)))
	for _, m := range matches[:min(limit, len(matches))] {
		names = append(names, m.name)
	}
	return names, nil
}
