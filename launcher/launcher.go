package launcher

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// SearchResult is a single item returned by the OnSearch callback.
type SearchResult struct {
	Name        string
	Description string
	IconName    string // optional; if empty, no icon is sent
}

// Config configures a launcher plugin.
type Config struct {
	Stdin      io.Reader   // if nil, defaults to os.Stdin
	Stdout     io.Writer   // if nil, defaults to os.Stdout
	Logger     *log.Logger // if nil, logging is discarded
	OnSearch   func(ctx context.Context, query string, appendResult func(SearchResult)) error
	OnActivate func(entry string) error
}

// LoadConfig validates required callbacks and prevents runtime panics and loads the config.
func (c *Config) LoadConfig() (*log.Logger, io.Reader, io.Writer, error) {
	l := log.New(io.Discard, "", 0)

	if c == nil {
		return l, nil, nil, fmt.Errorf("config is nil")
	}

	if c.Logger != nil {
		l = c.Logger
	}

	if c.OnSearch == nil {
		return l, nil, nil, fmt.Errorf("config OnSearch callback is required")
	}
	if c.OnActivate == nil {
		return l, nil, nil, fmt.Errorf("config OnActivate callback is required")
	}

	stdin := c.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := c.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	return l, stdin, stdout, nil
}

// Run reads pop-launcher requests from stdin and writes responses to stdout.
// It blocks until stdin is closed or an "Exit" request is received.
func Run(cfg Config) {
	l, stdin, stdout, err := cfg.LoadConfig()
	if err != nil {
		l.Printf("ERROR: invalid launcher config: %v", err)
		return
	}

	var (
		outputMu     sync.Mutex
		resultsMu    sync.Mutex
		lastResults  []string
		searchCancel context.CancelFunc
		searchDone   chan struct{}
	)

	respond := func(v any) {
		outputMu.Lock()
		defer outputMu.Unlock()
		data, err := json.Marshal(v)
		if err != nil {
			l.Printf("ERROR: failed to marshal response: %v", err)
			return
		}
		l.Println(string(data))
		stdout.Write(data)
		stdout.Write([]byte{'\n'})
	}

	respondRaw := func(s string) {
		outputMu.Lock()
		defer outputMu.Unlock()
		l.Println(s)
		fmt.Fprintln(stdout, s)
	}

	cancelSearch := func() {
		if searchCancel != nil {
			searchCancel()
			<-searchDone
			searchCancel = nil
			searchDone = nil
		}
	}

	requests := make(chan string, 64)

	go func() {
		scanner := bufio.NewScanner(stdin)
		for scanner.Scan() {
			requests <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			l.Printf("ERROR: stdin read error: %v", err)
		}
		close(requests)
	}()

	for line := range requests {
		l.Println("Received request: " + line)
		trimmed := strings.TrimSpace(line)
		if trimmed == `"Exit"` {
			l.Println("Exiting")
			cancelSearch()
			defer respondRaw(`"Finished"`)
			return
		}
		if trimmed == `"Interrupt"` {
			l.Println("Interrupted")
			wasSearching := searchCancel != nil
			cancelSearch()
			if !wasSearching {
				respondRaw(`"Finished"`)
			}
			continue
		}

		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			l.Printf("ERROR: failed to parse request: %v", err)
			continue
		}

		switch {
		case req.Search != nil:
			cancelSearch()

			query := *req.Search

			ctx, cancel := context.WithCancel(context.Background())
			searchCancel = cancel
			done := make(chan struct{})
			searchDone = done

			go func(ctx context.Context, query string) {
				defer close(done)
				defer respondRaw(`"Finished"`)

				respondRaw(`"Clear"`)

				var matched []string

				appendResult := func(sr SearchResult) {
					var icon *iconSource
					if sr.IconName != "" {
						icon = &iconSource{Name: &sr.IconName}
					}
					respond(appendResponse{
						Append: pluginSearchResult{
							ID:          uint32(len(matched)),
							Name:        sr.Name,
							Description: sr.Description,
							Icon:        icon,
						},
					})
					matched = append(matched, sr.Name)
				}

				if err := cfg.OnSearch(ctx, query, appendResult); err != nil {
					l.Printf("ERROR: search failed: %v", err)
				}

				resultsMu.Lock()
				lastResults = matched
				resultsMu.Unlock()
			}(ctx, query)

		case req.Activate != nil:
			cancelSearch()

			id := int(*req.Activate)
			resultsMu.Lock()
			var entry string
			if id < len(lastResults) {
				entry = lastResults[id]
				resultsMu.Unlock()
			} else {
				l.Printf("ERROR: Activate id=%d out of range (have %d results)", id, len(lastResults))
				resultsMu.Unlock()
				respondRaw(`"Close"`)
				continue
			}

			if err := cfg.OnActivate(entry); err != nil {
				l.Printf("ERROR: activate failed: %v", err)
			}
			respondRaw(`"Close"`)

		default:
			l.Printf("Unhandled request: %s", line)
		}
	}
}

// Unexported protocol types for pop-launcher JSON IPC.

type request struct {
	Search   *string `json:"Search,omitempty"`
	Activate *uint32 `json:"Activate,omitempty"`
	Complete *uint32 `json:"Complete,omitempty"`
	Context  *uint32 `json:"Context,omitempty"`
}

type iconSource struct {
	Name *string `json:"Name,omitempty"`
}

type pluginSearchResult struct {
	ID          uint32      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Icon        *iconSource `json:"icon,omitempty"`
}

type appendResponse struct {
	Append pluginSearchResult `json:"Append"`
}
