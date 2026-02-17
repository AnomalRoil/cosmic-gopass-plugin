package launcher

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
)

// safeWriter is a thread-safe writer for capturing concurrent output.
type safeWriter struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (w *safeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *safeWriter) Lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := strings.TrimRight(w.buf.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// runTrace feeds input lines into Run and returns the collected output lines
// and log output. The OnSearch/OnActivate callbacks are provided by the caller
// to control the behavior of each test scenario.
func runTrace(
	t *testing.T,
	input []string,
	onSearch func(context.Context, string, func(SearchResult)) error,
	onActivate func(string) error,
) (outputLines []string, logOutput string) {
	t.Helper()

	stdin := strings.NewReader(strings.Join(input, "\n") + "\n")
	var stdout safeWriter
	var logBuf strings.Builder

	Run(Config{
		Stdin:      stdin,
		Stdout:     &stdout,
		Logger:     log.New(&logBuf, "", 0),
		OnSearch:   onSearch,
		OnActivate: onActivate,
	})

	return stdout.Lines(), logBuf.String()
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("output line count: got %d, want %d\n  got:  %q\n  want: %q",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n  got:  %s\n  want: %s", i, got[i], want[i])
		}
	}
}

// --- LoadConfig tests ---

func TestLoadConfigNilReceiver(t *testing.T) {
	var c *Config
	_, _, _, err := c.LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "config is nil") {
		t.Fatalf("expected 'config is nil' error, got: %v", err)
	}
}

func TestLoadConfigMissingOnSearch(t *testing.T) {
	c := &Config{
		OnActivate: func(string) error { return nil },
	}
	_, _, _, err := c.LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "OnSearch") {
		t.Fatalf("expected OnSearch error, got: %v", err)
	}
}

func TestLoadConfigMissingOnActivate(t *testing.T) {
	c := &Config{
		OnSearch: func(context.Context, string, func(SearchResult)) error { return nil },
	}
	_, _, _, err := c.LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "OnActivate") {
		t.Fatalf("expected OnActivate error, got: %v", err)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	c := &Config{
		OnSearch:   func(context.Context, string, func(SearchResult)) error { return nil },
		OnActivate: func(string) error { return nil },
	}
	l, stdin, stdout, err := c.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l == nil {
		t.Error("expected non-nil default logger")
	}
	if stdin == nil {
		t.Error("expected non-nil default stdin")
	}
	if stdout == nil {
		t.Error("expected non-nil default stdout")
	}
}

func TestLoadConfigCustomValues(t *testing.T) {
	customLogger := log.New(io.Discard, "test:", 0)
	customStdin := strings.NewReader("")
	var customStdout strings.Builder

	c := &Config{
		Stdin:      customStdin,
		Stdout:     &customStdout,
		Logger:     customLogger,
		OnSearch:   func(context.Context, string, func(SearchResult)) error { return nil },
		OnActivate: func(string) error { return nil },
	}
	l, stdin, stdout, err := c.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l != customLogger {
		t.Error("expected custom logger to be returned as-is")
	}
	if stdin != customStdin {
		t.Error("expected custom stdin to be returned as-is")
	}
	if stdout != &customStdout {
		t.Error("expected custom stdout to be returned as-is")
	}
}

func TestRunWithInvalidConfig(t *testing.T) {
	var logBuf strings.Builder
	Run(Config{
		Logger: log.New(&logBuf, "", 0),
	})

	if !strings.Contains(logBuf.String(), "ERROR: invalid launcher config") {
		t.Errorf("expected 'config is nil' error, got: %s", logBuf.String())
	}
}

// --- IPC trace tests ---

func TestExitImmediately(t *testing.T) {
	got, _ := runTrace(t,
		// Input trace:
		[]string{`"Exit"`},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			t.Error("OnSearch called unexpectedly")
			return nil
		},
		func(entry string) error {
			t.Error("OnActivate called unexpectedly")
			return nil
		},
	)

	// Expected output trace:
	assertLines(t, got, []string{
		`"Finished"`,
	})
}

func TestSearchThenExit(t *testing.T) {
	got, _ := runTrace(t,
		// Input trace:
		[]string{
			`{"Search":"gp email"}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			if q != "gp email" {
				t.Errorf("query = %q, want %q", q, "gp email")
			}
			add(SearchResult{Name: "Email/personal", Description: "Email/personal", IconName: "dialog-password"})
			add(SearchResult{Name: "Email/work", Description: "Email/work", IconName: "dialog-password"})
			return nil
		},
		func(entry string) error {
			t.Error("OnActivate called unexpectedly")
			return nil
		},
	)

	// Expected output trace:
	assertLines(t, got, []string{
		`"Clear"`,
		`{"Append":{"id":0,"name":"Email/personal","description":"Email/personal","icon":{"Name":"dialog-password"}}}`,
		`{"Append":{"id":1,"name":"Email/work","description":"Email/work","icon":{"Name":"dialog-password"}}}`,
		`"Finished"`,
		`"Finished"`,
	})
}

func TestSearchActivateExit(t *testing.T) {
	var activated string
	got, _ := runTrace(t,
		// Input trace:
		[]string{
			`{"Search":"gp email"}`,
			`{"Activate":0}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			add(SearchResult{Name: "Email/personal", Description: "Email/personal", IconName: "dialog-password"})
			add(SearchResult{Name: "Email/work", Description: "Email/work", IconName: "dialog-password"})
			return nil
		},
		func(entry string) error {
			activated = entry
			return nil
		},
	)

	if activated != "Email/personal" {
		t.Errorf("activated entry = %q, want %q", activated, "Email/personal")
	}

	// Expected output trace:
	assertLines(t, got, []string{
		`"Clear"`,
		`{"Append":{"id":0,"name":"Email/personal","description":"Email/personal","icon":{"Name":"dialog-password"}}}`,
		`{"Append":{"id":1,"name":"Email/work","description":"Email/work","icon":{"Name":"dialog-password"}}}`,
		`"Finished"`,
		`"Close"`,
		`"Finished"`,
	})
}

func TestActivateSecondResult(t *testing.T) {
	var activated string
	got, _ := runTrace(t,
		// Input trace:
		[]string{
			`{"Search":"gp email"}`,
			`{"Activate":1}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			add(SearchResult{Name: "Email/personal", Description: "personal email"})
			add(SearchResult{Name: "Email/work", Description: "work email"})
			return nil
		},
		func(entry string) error {
			activated = entry
			return nil
		},
	)

	if activated != "Email/work" {
		t.Errorf("activated entry = %q, want %q", activated, "Email/work")
	}

	// Expected output trace:
	assertLines(t, got, []string{
		`"Clear"`,
		`{"Append":{"id":0,"name":"Email/personal","description":"personal email"}}`,
		`{"Append":{"id":1,"name":"Email/work","description":"work email"}}`,
		`"Finished"`,
		`"Close"`,
		`"Finished"`,
	})
}

func TestSearchNoIcon(t *testing.T) {
	got, _ := runTrace(t,
		// Input trace:
		[]string{
			`{"Search":"q"}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			add(SearchResult{Name: "plain-entry", Description: "no icon here"})
			return nil
		},
		func(entry string) error { return nil },
	)

	// Expected output trace (no "icon" field):
	assertLines(t, got, []string{
		`"Clear"`,
		`{"Append":{"id":0,"name":"plain-entry","description":"no icon here"}}`,
		`"Finished"`,
		`"Finished"`,
	})
}

func TestEmptySearchResults(t *testing.T) {
	got, _ := runTrace(t,
		// Input trace:
		[]string{
			`{"Search":"nothing-matches"}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			return nil
		},
		func(entry string) error { return nil },
	)

	// Expected output trace:
	assertLines(t, got, []string{
		`"Clear"`,
		`"Finished"`,
		`"Finished"`,
	})
}

func TestConsecutiveSearches(t *testing.T) {
	got, _ := runTrace(t,
		// Input trace: second search replaces the first.
		[]string{
			`{"Search":"first"}`,
			`{"Search":"second"}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			add(SearchResult{Name: "result-" + q, Description: q})
			return nil
		},
		func(entry string) error { return nil },
	)

	// Expected output trace: both searches produce full output.
	assertLines(t, got, []string{
		`"Clear"`,
		`{"Append":{"id":0,"name":"result-first","description":"first"}}`,
		`"Finished"`,
		`"Clear"`,
		`{"Append":{"id":0,"name":"result-second","description":"second"}}`,
		`"Finished"`,
		`"Finished"`,
	})
}

func TestInterruptWithoutActiveSearch(t *testing.T) {
	got, _ := runTrace(t,
		// Input trace:
		[]string{
			`"Interrupt"`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			t.Error("OnSearch called unexpectedly")
			return nil
		},
		func(entry string) error {
			t.Error("OnActivate called unexpectedly")
			return nil
		},
	)

	// Expected output trace: Interrupt with no search sends its own Finished.
	assertLines(t, got, []string{
		`"Finished"`,
		`"Finished"`,
	})
}

func TestActivateOutOfRange(t *testing.T) {
	got, logOut := runTrace(t,
		// Input trace:
		[]string{
			`{"Search":"q"}`,
			`{"Activate":99}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			add(SearchResult{Name: "only-one", Description: "single result", IconName: "dialog-password"})
			return nil
		},
		func(entry string) error {
			t.Error("OnActivate should not be called for out-of-range ID")
			return nil
		},
	)

	// Expected output trace:
	assertLines(t, got, []string{
		`"Clear"`,
		`{"Append":{"id":0,"name":"only-one","description":"single result","icon":{"Name":"dialog-password"}}}`,
		`"Finished"`,
		`"Close"`,
		`"Finished"`,
	})

	if !strings.Contains(logOut, "out of range") {
		t.Errorf("log should mention 'out of range', got: %s", logOut)
	}
}

func TestActivateCallbackError(t *testing.T) {
	got, logOut := runTrace(t,
		// Input trace:
		[]string{
			`{"Search":"q"}`,
			`{"Activate":0}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			add(SearchResult{Name: "entry", Description: "desc"})
			return nil
		},
		func(entry string) error {
			return fmt.Errorf("paste failed: device busy")
		},
	)

	// "Close" is still sent even when OnActivate returns an error.
	assertLines(t, got, []string{
		`"Clear"`,
		`{"Append":{"id":0,"name":"entry","description":"desc"}}`,
		`"Finished"`,
		`"Close"`,
		`"Finished"`,
	})

	if !strings.Contains(logOut, "paste failed") {
		t.Errorf("log should contain callback error, got: %s", logOut)
	}
}

func TestUnhandledRequest(t *testing.T) {
	got, logOut := runTrace(t,
		// Input trace: Complete is parsed but not handled.
		[]string{
			`{"Complete":0}`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			t.Error("OnSearch called unexpectedly")
			return nil
		},
		func(entry string) error {
			t.Error("OnActivate called unexpectedly")
			return nil
		},
	)

	assertLines(t, got, []string{
		`"Finished"`,
	})

	if !strings.Contains(logOut, "Unhandled") {
		t.Errorf("log should mention 'Unhandled', got: %s", logOut)
	}
}

func TestInvalidJSON(t *testing.T) {
	got, logOut := runTrace(t,
		// Input trace: malformed line then exit.
		[]string{
			`not valid json at all`,
			`"Exit"`,
		},
		func(ctx context.Context, q string, add func(SearchResult)) error {
			t.Error("OnSearch called unexpectedly")
			return nil
		},
		func(entry string) error {
			t.Error("OnActivate called unexpectedly")
			return nil
		},
	)

	assertLines(t, got, []string{
		`"Finished"`,
	})

	if !strings.Contains(logOut, "failed to parse") {
		t.Errorf("log should mention 'failed to parse', got: %s", logOut)
	}
}

// TestSearchInterruptCancelsContext uses io.Pipe for precise synchronization
// to verify that interrupting an active search cancels the context passed to
// OnSearch.
func TestSearchInterruptCancelsContext(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	searchStarted := make(chan struct{})

	cfg := Config{
		Stdin:  stdinR,
		Stdout: stdoutW,
		Logger: log.New(io.Discard, "", 0),
		OnSearch: func(ctx context.Context, q string, add func(SearchResult)) error {
			close(searchStarted)
			<-ctx.Done()
			return ctx.Err()
		},
		OnActivate: func(entry string) error {
			t.Error("OnActivate called unexpectedly")
			return nil
		},
	}

	var got []string
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		scanner := bufio.NewScanner(stdoutR)
		for scanner.Scan() {
			got = append(got, scanner.Text())
		}
	}()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		Run(cfg)
		stdoutW.Close()
	}()

	// Send a search that will block until its context is cancelled.
	fmt.Fprintln(stdinW, `{"Search":"slow-query"}`)
	<-searchStarted

	// Interrupt cancels the search context; then exit.
	fmt.Fprintln(stdinW, `"Interrupt"`)
	fmt.Fprintln(stdinW, `"Exit"`)
	stdinW.Close()

	<-runDone
	<-outputDone

	// Expected output trace: Clear from the search, Finished from the search
	// goroutine (no results since OnSearch blocked), Finished from Exit.
	assertLines(t, got, []string{
		`"Clear"`,
		`"Finished"`,
		`"Finished"`,
	})
}
