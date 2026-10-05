package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/syslog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/AnomalRoil/cosmic-gopass-plugin/autotype"
	"github.com/AnomalRoil/cosmic-gopass-plugin/launcher"
)

var gopassPath string

const (
	maxResults  = 20
	reloadAfter = time.Minute
)

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

func loadEntries() ([]entry, error) {
	out, err := exec.Command(gopassPath, "--nosync", "ls", "-flat").Output()
	if err != nil {
		return nil, fmt.Errorf("gopass ls: %w", err)
	}
	return newEntries(strings.Split(string(out), "\n")), nil
}

func describe(q query, name string) string {
	switch q.mode {
	case modeKey:
		return fmt.Sprintf("Copy %s to clipboard", q.key)
	case modeOTP:
		return "Copy OTP to clipboard"
	case modeName:
		return fmt.Sprintf("Copy name %q to clipboard", path.Base(name))
	default:
		return "Copy password to clipboard"
	}
}

// selection is the launcher.SearchResult payload.
type selection struct {
	name  string
	query query
}

func copyToClipboard(sel selection) error {
	var cmd *exec.Cmd
	switch sel.query.mode {
	case modeKey:
		cmd = exec.Command(gopassPath, "--nosync", "show", "-C=false", "-c=true", sel.name, sel.query.key)
	case modeOTP:
		cmd = exec.Command(gopassPath, "--nosync", "otp", "--clip", sel.name)
	case modeName:
		// wl-copy forks a daemon that inherits stdout and stderr, so capturing them would block forever.
		cmd = exec.Command("wl-copy")
		cmd.Stdin = strings.NewReader(path.Base(sel.name))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("wl-copy: %w", err)
		}
		return nil
	default:
		cmd = exec.Command(gopassPath, "--nosync", "show", "-C=false", "-c=true", "-o", sel.name)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, bytes.TrimSpace(out))
	}
	return nil
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
		defer syslogWriter.Close() //nolint:errcheck // nothing left to log to
	}

	if len(os.Args) > 1 && os.Args[1] == "paste" {
		if err := autotype.PressPaste(); err != nil {
			log.Printf("ERROR: paste failed: %v", err)
			os.Exit(1)
		}
		return
	}

	gopassPath = findGopass()
	log.Printf("Gopass plugin started as user=%s HOME=%s gopass=%s", os.Getenv("USER"), os.Getenv("HOME"), gopassPath)
	defer log.Println("Gopass plugin stopped")

	// Searches never run concurrently, so entries and loadedAt need no lock.
	var (
		entries  []entry
		loadedAt time.Time
	)

	launcher.Run(launcher.Config{
		Logger: log.Default(),
		OnSearch: func(ctx context.Context, raw string, appendResult func(launcher.SearchResult)) error {
			if time.Since(loadedAt) > reloadAfter {
				fresh, err := loadEntries()
				if err != nil {
					log.Printf("ERROR: keeping %d cached entries: %v", len(entries), err)
				} else {
					entries, loadedAt = fresh, time.Now()
				}
			}
			q := parseQuery(raw)
			names, err := search(ctx, entries, q.terms, maxResults)
			if err != nil {
				return err
			}
			for _, name := range names {
				appendResult(launcher.SearchResult{
					Name:        name,
					Description: describe(q, name),
					IconName:    "dialog-password",
					Payload:     selection{name: name, query: q},
				})
			}
			return nil
		},
		OnActivate: func(r launcher.SearchResult) error {
			sel := r.Payload.(selection)
			if err := copyToClipboard(sel); err != nil {
				return err
			}
			// A separate process group lets the paste outlive the plugin once the launcher closes.
			paste := exec.Command(os.Args[0], "paste")
			paste.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := paste.Start(); err != nil {
				return fmt.Errorf("start '%s paste': %w", os.Args[0], err)
			}
			go paste.Wait() //nolint:errcheck // the paste process logs its own failures
			log.Printf("Activated %s: %s, pasting (pid %d)", sel.name, describe(sel.query, sel.name), paste.Process.Pid)
			return nil
		},
	})
}
