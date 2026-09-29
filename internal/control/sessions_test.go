package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

// sessionExec simulates a tmux server, including client discovery after attach.
// Keeping server state behind its own lock lets the concurrency test exercise
// the real filesystem attach lock instead of encoding the expected ordering.
type sessionExec struct {
	mu          sync.Mutex
	rows        map[string]string
	clientReads map[string]int
	calls       [][]string
	createErr   bool
}

func (f *sessionExec) Exec(_ context.Context, _ string, cmd []string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), cmd...))
	if cmd[0] == "sh" {
		return "yes\n", 0, nil
	}
	if len(cmd) > 1 && cmd[1] == "list-sessions" {
		var out []string
		for _, row := range f.rows {
			out = append(out, row)
		}
		return strings.Join(out, "\n"), 0, nil
	}
	if len(cmd) > 1 && cmd[1] == "list-clients" {
		name := cmd[3]
		if f.clientReads == nil {
			f.clientReads = map[string]int{}
		}
		f.clientReads[name]++
		if f.clientReads[name] == 1 {
			return "", 0, nil
		}
		return "/dev/pts/" + strings.TrimPrefix(name, SessionClaude) + "7\n", 0, nil
	}
	if len(cmd) > 1 && cmd[1] == "detach-client" {
		return "", 0, nil
	}
	if strings.Contains(strings.Join(cmd, " "), "new-session") {
		// Real tmux rejects '=name' for set-option and produces an empty
		// context for display-message; accepting it here hid a live failure.
		for i, arg := range cmd {
			if arg == "-t" && strings.HasPrefix(cmd[i+1], "=") {
				return "no such session: " + cmd[i+1], 1, nil
			}
		}
		if f.createErr {
			return "create failed", 1, nil
		}
		name, token := "", ""
		for i, arg := range cmd {
			if arg == "-s" {
				name = cmd[i+1]
			}
			if arg == "@cspace_id" {
				token = cmd[i+1]
			}
		}
		if _, exists := f.rows[name]; exists {
			return "duplicate session", 1, nil
		}
		if f.rows == nil {
			f.rows = map[string]string{}
		}
		row := fmt.Sprintf("%s:$%d:1720000000:100:1719999999:%s", name, len(f.rows), token)
		f.rows[name] = row
		return row + "\n", 0, nil
	}
	return "unexpected command", 1, nil
}

func sessionTmux(f Execer) *Tmux {
	tm := NewTmux()
	tm.Exec, tm.PollEvery, tm.PollFor = f, time.Millisecond, time.Second
	return tm
}

func TestSessionFormatsSurviveTmuxWithoutUTF8Mode(t *testing.T) {
	// A live tmux query without -u turned a literal-tab record into
	// cspace-claude_$0_1790712000_408_1790712000_. Container exec itself
	// preserved tabs; the replacement happens in tmux's format output.
	if strings.IndexFunc(sessionListFormat, unicode.IsControl) >= 0 {
		t.Fatalf("machine format contains a control-character separator: %q", sessionListFormat)
	}
	f := &fakeExec{reply: func(_ int, cmd []string) (string, int, error) {
		if cmd[0] == "sh" {
			return "yes", 0, nil
		}
		format := cmd[len(cmd)-1]
		if format != sessionListFormat {
			t.Fatalf("unexpected discovery format: %q", format)
		}
		record := strings.NewReplacer("#{session_name}", SessionClaude, "#{session_id}", "$0",
			"#{session_created}", "1790712000", "#{pid}", "408", "#{start_time}", "1790712000", "#{@cspace_id}", "").Replace(format)
		record = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return '_'
			}
			return r
		}, record)
		return record + "\n", 0, nil
	}}
	got, err := sessionTmux(f).Sessions(context.Background(), "ct")
	if err != nil || len(got) != 1 || got[0].Name != SessionClaude || got[0].ID != "408/1790712000/$0/1790712000/" || got[0].stateID != "" {
		t.Fatalf("legacy record with empty token was lost: sessions=%+v err=%v", got, err)
	}
}

func TestPrepareDefaultClaudeCreatesOnceAndPinsEveryClient(t *testing.T) {
	f := &sessionExec{}
	tm, home := sessionTmux(f), t.TempDir()
	var identity string
	for i := 0; i < 2; i++ {
		p, err := PrepareClaudeAttach(context.Background(), tm, home, "alpha", "mercury", "ct", AttachRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Session.Name != SessionClaude || p.Session.ID == "" || p.Spec.ExistingSession == nil || p.Spec.ExistingSession.ID != p.Session.ID {
			t.Fatalf("default client has no pinned identity: %+v", p)
		}
		if identity != "" && p.Session.ID != identity {
			t.Fatalf("second default attach replaced the session: %s -> %s", identity, p.Session.ID)
		}
		identity = p.Session.ID
		if err := p.Attachment.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	creates := 0
	for _, cmd := range f.calls {
		if strings.Contains(strings.Join(cmd, " "), "new-session") {
			creates++
			for _, arg := range cmd {
				if strings.HasPrefix(arg, "CSPACE_AGENT_STATE_FILE=") {
					t.Fatalf("default changed the legacy hook location: %v", cmd)
				}
			}
		}
	}
	if creates != 1 {
		t.Fatalf("default created %d sessions, want 1", creates)
	}
}

func TestPrepareNewClaudeSessionsAllocatesUnderTheAttachLock(t *testing.T) {
	f := &sessionExec{}
	tm, home := sessionTmux(f), t.TempDir()
	start := make(chan struct{})
	results := make(chan PreparedAttach, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			p, err := PrepareClaudeAttach(context.Background(), tm, home, "alpha", "mercury", "ct", AttachRequest{New: true})
			if err == nil {
				results <- p
			} else {
				errs <- err
			}
		}()
	}
	close(start)
	var prepared []PreparedAttach
	for i := 0; i < 2; i++ {
		select {
		case p := <-results:
			prepared = append(prepared, p)
		case err := <-errs:
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent session creation blocked")
		}
	}
	for _, p := range prepared {
		defer func() { _ = p.Attachment.Close(context.Background()) }()
	}
	if prepared[0].Session.Name == prepared[1].Session.Name || prepared[0].Session.ID == prepared[1].Session.ID {
		t.Fatalf("concurrent new requests shared a process: %+v / %+v", prepared[0].Session, prepared[1].Session)
	}
	names := map[string]bool{prepared[0].Session.Name: true, prepared[1].Session.Name: true}
	if !names["cspace-claude-2"] || !names["cspace-claude-3"] {
		t.Fatalf("names = %v", names)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var files []string
	for _, cmd := range f.calls {
		if !strings.Contains(strings.Join(cmd, " "), "new-session") {
			continue
		}
		for _, arg := range cmd {
			if strings.Contains(arg, "#{session_name}") && (arg != sessionListFormat || strings.IndexFunc(arg, unicode.IsControl) >= 0) {
				t.Fatalf("creation uses a different or nonprintable record format: %q", arg)
			}
			if arg == "-A" {
				t.Fatal("new session used attach-or-create")
			}
			if strings.HasPrefix(arg, "CSPACE_AGENT_STATE_FILE=") {
				files = append(files, arg)
			}
		}
	}
	if len(files) != 2 || files[0] == files[1] {
		t.Fatalf("new sessions did not get separate hook paths: %v", files)
	}
}

func TestSessionsListsOnlyManagedClaudeSessionsAndReadsSeparateStates(t *testing.T) {
	const id2 = "11111111111111111111111111111111"
	const id3 = "22222222222222222222222222222222"
	f := &sessionExec{rows: map[string]string{
		"default": "cspace-claude:$0:1720000000:100:1719999999:",
		"third":   "cspace-claude-3:$3:1720000001:100:1719999999:" + id3,
		"second":  "cspace-claude-2:$2:1720000001:100:1719999999:" + id2,
		"shell":   "cspace-shell:$1:1720000000:100:1719999999:",
		"user":    "my-claude:$4:1720000000:100:1719999999:",
		"pipe":    "cspace-claude|custom:$5:1720000000:100:1719999999:",
	}}
	home := t.TempDir()
	dir := filepath.Join(SessionDir(home, "alpha", "mercury"), "interactive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, state := range map[string]string{id2: "working", id3: "needs-input"} {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(`{"state":"`+state+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(AgentStatePath(home, "alpha", "mercury"), []byte(`{"state":"idle","at":"2026-09-29T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{Home: home, Tmux: sessionTmux(f)})
	sessions, err := c.Sessions(context.Background(), "alpha", "mercury")
	if err != nil {
		t.Fatal(err)
	}
	var labels, states []string
	for _, s := range sessions {
		labels = append(labels, s.Label())
		states = append(states, s.State.State)
	}
	if !reflect.DeepEqual(labels, []string{"Claude 1", "Claude 2", "Claude 3"}) {
		t.Fatalf("labels = %v", labels)
	}
	if !reflect.DeepEqual(states, []string{"idle", "working", "needs-input"}) {
		t.Fatalf("states = %v", states)
	}
	// Same display name, new incarnation: it must not read an old state file.
	newSession := sessions[1]
	newSession.stateID = "33333333333333333333333333333333"
	if got := c.SessionState("alpha", "mercury", newSession); got.Known() {
		t.Fatalf("replacement borrowed old state: %+v", got)
	}
}

func TestPrepareExistingSessionNeverRecreatesADisappearedOrReplacedSession(t *testing.T) {
	for _, tc := range []struct{ name, row, expected string }{
		{"disappeared", "", "100/1719999999/$0/1720000000/"},
		{"new server", "cspace-claude:$0:1720000000:200:1720000000:", "100/1719999999/$0/1720000000/"},
		{"new session", "cspace-claude:$9:1720000002:100:1719999999:", "100/1719999999/$0/1720000000/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &sessionExec{rows: map[string]string{"row": tc.row}}
			home := t.TempDir()
			_, err := PrepareClaudeAttach(context.Background(), sessionTmux(f), home, "alpha", "mercury", "ct", AttachRequest{Session: SessionClaude, ExpectedID: tc.expected})
			if !errors.Is(err, ErrSessionGone) {
				t.Fatalf("error = %v", err)
			}
			for _, cmd := range f.calls {
				if strings.Contains(strings.Join(cmd, " "), "new-session") {
					t.Fatal("existing-session request created a new process")
				}
			}
			lock, err := lockAttach(context.Background(), ControlPlaneDir(home, "alpha", "mercury"), time.Millisecond)
			if err != nil {
				t.Fatal("failed request kept attach lock:", err)
			}
			a := &Attachment{lock: lock}
			a.releaseLock()
		})
	}
}

func TestPrepareSessionRequiresTmuxAndReleasesLockAfterCreationFailure(t *testing.T) {
	missing := sessionTmux(&fakeExec{reply: func(int, []string) (string, int, error) { return "no", 0, nil }})
	if _, err := PrepareClaudeAttach(context.Background(), missing, t.TempDir(), "alpha", "mercury", "ct", AttachRequest{New: true}); !errors.Is(err, ErrTmuxRequired) {
		t.Fatalf("no-tmux error = %v", err)
	}
	f, home := &sessionExec{createErr: true}, t.TempDir()
	tm := sessionTmux(f)
	if _, err := PrepareClaudeAttach(context.Background(), tm, home, "alpha", "mercury", "ct", AttachRequest{New: true}); err == nil {
		t.Fatal("create failure ignored")
	}
	f.createErr = false
	p, err := PrepareClaudeAttach(context.Background(), tm, home, "alpha", "mercury", "ct", AttachRequest{New: true})
	if err != nil {
		t.Fatalf("failed creation stranded lock: %v", err)
	}
	_ = p.Attachment.Close(context.Background())
}

func TestExistingAttachArgvPinsTheSessionInsideTmux(t *testing.T) {
	// Only binary resolution is needed; the argv test never executes it.
	binDir := t.TempDir()
	containerBin := filepath.Join(binDir, "container")
	if err := os.WriteFile(containerBin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	sessions, err := parseSessions("cspace-claude-2:$5:1720000000:100:1719999999:11111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	s := sessions[0]
	spec := ClaudeAttach("ct", true)
	spec.Session, spec.ExistingSession = s.Name, &s
	bin, argv, err := AttachArgv(spec)
	if err != nil {
		t.Fatal(err)
	}
	if bin != containerBin {
		t.Fatalf("bin = %q, want test executable %q", bin, containerBin)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"if-shell -F -t $5", s.ID, "attach-session -t '$5'", "run-shell 'exit 1'"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %q: %v", want, argv)
		}
	}
	if strings.Contains(joined, "new-session") || strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Fatalf("attach-only could create a process: %v", argv)
	}
}

func TestSessionRequestsRejectUnsafeTargets(t *testing.T) {
	for _, name := range []string{"../escape", "cspace-claude:1", "cspace-claude*", "cspace-claude-1", "cspace-claude-02", "cspace-claude-2;kill-server", "cspace-claude-9999999999999999999999999999999"} {
		if err := (AttachRequest{Session: name}).Validate(); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	if err := (AttachRequest{New: true, Session: SessionClaude}).Validate(); err == nil {
		t.Fatal("accepted new+session")
	}
}

func TestSessionDiscoveryDoesNotMistakeFailureForAnEmptyList(t *testing.T) {
	f := &fakeExec{reply: func(_ int, cmd []string) (string, int, error) {
		if cmd[0] == "sh" {
			return "yes", 0, nil
		}
		return "container is not running", 1, nil
	}}
	if _, err := sessionTmux(f).Sessions(context.Background(), "ct"); err == nil {
		t.Fatal("failed container query reported no sessions")
	}
	f.reply = func(_ int, cmd []string) (string, int, error) {
		if cmd[0] == "sh" {
			return "yes", 0, nil
		}
		return "no server running on /tmp/tmux-1000/default", 1, nil
	}
	if got, err := sessionTmux(f).Sessions(context.Background(), "ct"); err != nil || len(got) != 0 {
		t.Fatalf("no-server = (%v,%v)", got, err)
	}
}
