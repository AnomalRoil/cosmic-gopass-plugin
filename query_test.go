package main

import (
	"context"
	"slices"
	"testing"
)

func TestParseQuery(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want query
	}{
		{"empty", "gp ", query{}},
		{"terms", "gp Git  Hub", query{terms: []string{"git", "hub"}}},
		{"otp short", "gp -o github", query{mode: modeOTP, terms: []string{"github"}}},
		{"otp long", "gp github -otp", query{mode: modeOTP, terms: []string{"github"}}},
		{"name short", "gp -n github", query{mode: modeName, terms: []string{"github"}}},
		{"name long", "gp -name github", query{mode: modeName, terms: []string{"github"}}},
		{"key", "gp github :User", query{mode: modeKey, key: "user", terms: []string{"github"}}},
		{"last option wins", "gp :url github -o", query{mode: modeOTP, terms: []string{"github"}}},
		{"lone colon is a term", "gp :", query{terms: []string{":"}}},
		{"unknown dash is a term", "gp -x", query{terms: []string{"-x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseQuery(tt.in)
			if got.mode != tt.want.mode || got.key != tt.want.key || !slices.Equal(got.terms, tt.want.terms) {
				t.Errorf("parseQuery(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	entries := newEntries([]string{
		"work/github.com/alice",
		"personal/github.com",
		"github",
		"Github",
		"websites/gitlab.com/alice",
		"misc/hub-for-git",
		"",
	})
	tests := []struct {
		name  string
		terms []string
		limit int
		want  []string
	}{
		{"exact first, case-insensitive", []string{"github"}, 10, []string{"Github", "github", "personal/github.com", "work/github.com/alice"}},
		{"words in any order", []string{"alice", "git"}, 10, []string{"work/github.com/alice", "websites/gitlab.com/alice"}},
		{"base name hits rank first", []string{"git", "hub"}, 10, []string{"Github", "github", "misc/hub-for-git", "personal/github.com", "work/github.com/alice"}},
		{"limit", []string{"git"}, 2, []string{"Github", "github"}},
		{"no match", []string{"nope"}, 10, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := search(context.Background(), entries, tt.terms, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("search(%q) = %q, want %q", tt.terms, got, tt.want)
			}
		})
	}
}

func TestSearchCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := search(ctx, newEntries([]string{"a"}), nil, 10); err == nil {
		t.Error("search with cancelled context: got nil error")
	}
}
