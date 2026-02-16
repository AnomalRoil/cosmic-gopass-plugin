package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/syslog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	gopassPath   string
	outputMu     sync.Mutex
	passwordIcon = "dialog-password"
)

const maxResults = 19

type entry struct {
	original string
	lower    string
}

func findGopass() string {
	if p, err := exec.LookPath("gopass"); err == nil {
		return p
	}
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		p := filepath.Join(gobin, "gopass")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		p := filepath.Join(gopath, "bin", "gopass")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if home := os.Getenv("HOME"); home != "" {
		p := filepath.Join(home, "go", "bin", "gopass")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "gopass"
}

// Request types from pop-launcher
type Request struct {
	Search   *string `json:"Search,omitempty"`
	Activate *uint32 `json:"Activate,omitempty"`
	Complete *uint32 `json:"Complete,omitempty"`
	Context  *uint32 `json:"Context,omitempty"`
}

// PluginResponse types to pop-launcher
type IconSource struct {
	Name *string `json:"Name,omitempty"`
}

type PluginSearchResult struct {
	ID          uint32      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Icon        *IconSource `json:"icon,omitempty"`
}

type AppendResponse struct {
	Append PluginSearchResult `json:"Append"`
}

func respond(v any) {
	outputMu.Lock()
	defer outputMu.Unlock()
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("ERROR: failed to marshal response: %v", err)
		return
	}
	os.Stdout.Write(data)
	os.Stdout.Write([]byte{'\n'})
}

func respondRaw(s string) {
	outputMu.Lock()
	defer outputMu.Unlock()
	fmt.Println(s)
}

func loadEntries() []entry {
	log.Println("Loading gopass entries...")
	cmd := exec.Command(gopassPath, "--nosync", "ls", "-flat")
	out, err := cmd.Output()
	if err != nil {
		log.Printf("ERROR: gopass ls failed: %v", err)
		return nil
	}
	var entries []entry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			// we pre-compute the lower case version of the entry to avoid doing it in the loop
			entries = append(entries, entry{original: line, lower: strings.ToLower(line)})
		}
	}
	log.Printf("Loaded %d entries from gopass", len(entries))
	return entries
}

func main() {
	syslogWriter, err := syslog.New(syslog.LOG_INFO|syslog.LOG_USER, "gopass-plugin")
	if err != nil {
		log.SetOutput(os.Stderr)
		log.SetPrefix("gopass-plugin: ")
		log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
		log.Printf("WARNING: could not connect to syslog, logging to stderr: %v", err)
	} else {
		log.SetOutput(syslogWriter)
		log.SetPrefix("")
		log.SetFlags(0)
		defer syslogWriter.Close()
	}

	gopassPath = findGopass()
	log.Printf("Gopass plugin started as user=%s HOME=%s gopass=%s", os.Getenv("USER"), os.Getenv("HOME"), gopassPath)

	allEntries := loadEntries()

	var (
		lastResults  []string
		resultsMu    sync.Mutex
		searchCancel context.CancelFunc
		searchDone   chan struct{}
	)

	cancelSearch := func() {
		if searchCancel != nil {
			searchCancel()
			<-searchDone // wait for goroutine to send Finished and exit
			searchCancel = nil
			searchDone = nil
		}
	}

	requests := make(chan string, 64)

	// Read from stdin and send to requests channel in a goroutine
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			requests <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			log.Printf("ERROR: stdin read error: %v", err)
		}
		close(requests)
	}()

	// Process requests from the requests channel
	for line := range requests {
		trimmed := strings.TrimSpace(line)
		if trimmed == `"Exit"` {
			log.Println("Exiting")
			cancelSearch()
			defer respondRaw(`"Finished"`)
			return
		}
		if trimmed == `"Interrupt"` {
			log.Println("Interrupted")
			wasSearching := searchCancel != nil
			cancelSearch()
			if !wasSearching {
				respondRaw(`"Finished"`)
			}
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			log.Printf("ERROR: failed to parse request: %v", err)
			continue
		}

		switch {
		case req.Search != nil:
			cancelSearch()

			query := strings.TrimPrefix(*req.Search, "gp ")

			ctx, cancel := context.WithCancel(context.Background())
			searchCancel = cancel
			done := make(chan struct{})
			searchDone = done

			go func(ctx context.Context, query string) {
				defer close(done)
				defer respondRaw(`"Finished"`)

				respondRaw(`"Clear"`)

				lowerQuery := strings.ToLower(query)
				var matched []string

				for _, e := range allEntries {
					if ctx.Err() != nil {
						resultsMu.Lock()
						lastResults = matched
						resultsMu.Unlock()
						return
					}
					if lowerQuery == "" || strings.Contains(e.lower, lowerQuery) {
						respond(AppendResponse{
							Append: PluginSearchResult{
								ID:          uint32(len(matched)),
								Name:        e.original,
								Description: "Copy password to clipboard",
								Icon:        &IconSource{Name: &passwordIcon},
							},
						})
						matched = append(matched, e.original)
						if len(matched) >= maxResults {
							break
						}
					}
				}

				resultsMu.Lock()
				lastResults = matched
				resultsMu.Unlock()
			}(ctx, query)

		case req.Activate != nil:
			cancelSearch()

			id := int(*req.Activate)
			resultsMu.Lock()
			if id < len(lastResults) {
				entry := lastResults[id]
				resultsMu.Unlock()
				cmd := exec.Command(gopassPath, "show", "-C", entry)
				if out, err := cmd.CombinedOutput(); err != nil {
					log.Printf("ERROR: gopass show -C failed: %v, output: %s", err, string(out))
				}
			} else {
				resultsMu.Unlock()
				log.Printf("ERROR: Activate id=%d out of range (have %d results)", id, len(lastResults))
			}
			respondRaw(`"Close"`)

		default:
			log.Printf("Unhandled request: %s", line)
		}
	}
}
