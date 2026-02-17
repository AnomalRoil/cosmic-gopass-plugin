package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/syslog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AnomalRoil/cosmic-gopass-plugin/autotype"
	"github.com/AnomalRoil/cosmic-gopass-plugin/launcher"
)

var gopassPath string

const maxResults = 19

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

func loadEntries() map[string]string {
	log.Println("Loading gopass entries...")
	cmd := exec.Command(gopassPath, "--nosync", "ls", "-flat")
	out, err := cmd.Output()
	if err != nil {
		log.Printf("ERROR: gopass ls failed: %v", err)
		return nil
	}
	allEntries := strings.Split(strings.TrimSpace(string(out)), "\n")
	entries := make(map[string]string, len(allEntries))
	for _, line := range allEntries {
		if line != "" {
			// we pre-compute the lower case version of the entry to avoid doing it in the loop
			entries[strings.ToLower(line)] = line
		}
	}
	log.Printf("Loaded %d entries from gopass", len(entries))
	return entries
}

func main() {
	syslogWriter, err := syslog.New(syslog.LOG_DEBUG|syslog.LOG_USER, "gopass-plugin")
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

	if args := os.Args; len(args) > 1 && args[1] == "paste" {
		secret, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Printf("ERROR: reading secret from stdin: %v", err)
			os.Exit(1)
		}
		if len(secret) == 0 {
			log.Println("ERROR: stdin was empty, nothing to type")
			os.Exit(1)
		}

		if err := autotype.PressPaste(); err != nil {
			log.Printf("ERROR: typeString failed: %v", err)
		}
		os.Exit(0)
	}

	gopassPath = findGopass()
	log.Printf("Gopass plugin started as user=%s HOME=%s gopass=%s", os.Getenv("USER"), os.Getenv("HOME"), gopassPath)
	defer log.Println("Gopass plugin stopped")

	allEntries := loadEntries()

	launcher.Run(launcher.Config{
		Logger: log.Default(),
		OnSearch: func(ctx context.Context, query string, appendResult func(launcher.SearchResult)) error {
			query = strings.TrimPrefix(query, "gp ")
			lowerQuery := strings.ToLower(query)
			count := 0
			// we're using a map to avoid always displaying the same entries in the same order when refining the search
			// and to display an exact match first when it exists
			if exactMatch, ok := allEntries[lowerQuery]; ok {
				appendResult(launcher.SearchResult{
					Name:        exactMatch,
					Description: "Copy password to clipboard",
					IconName:    "dialog-password",
				})
			}
			for lower, original := range allEntries {
				if lower == lowerQuery {
					// done just above
					continue
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if lowerQuery == "" || strings.Contains(lower, lowerQuery) {
					appendResult(launcher.SearchResult{
						Name:        original,
						Description: "Copy password to clipboard",
						IconName:    "dialog-password",
					})
					count++
					if count >= maxResults {
						break
					}
				}
			}
			return nil
		},
		OnActivate: func(entry string) error {
			cmd := exec.Command(gopassPath, "--nosync", "show", "-C=false", "-c=true", "-o", entry)
			out, err := cmd.Output()
			if err != nil {
				return fmt.Errorf("gopass show -o failed: %w", err)
			}
			secret := strings.TrimSuffix(string(out), "\n")
			log.Printf("Retrieved password for entry %s, spawning paste process", entry)
			pasteCmd := exec.Command(os.Args[0], "paste")
			pasteCmd.SysProcAttr = &syscall.SysProcAttr{
				Setpgid: true,
			}
			pasteCmd.Stdin = strings.NewReader(secret)
			pasteCmd.Stdout = nil
			pasteCmd.Stderr = nil
			if err := pasteCmd.Start(); err != nil {
				return fmt.Errorf("'%s paste' start failed: %w", os.Args[0], err)
			}
			log.Printf("Started '%s paste' (pid %d) for entry %s", os.Args[0], pasteCmd.Process.Pid, entry)
			return nil
		},
	})
}
