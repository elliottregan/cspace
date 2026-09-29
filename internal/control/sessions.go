package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AttachRequest distinguishes creating a new Claude from joining an existing
// one. The zero value is the legacy attach-or-create behavior. ExpectedID pins
// a discovered session's incarnation; the CLI may omit it when naming a session.
type AttachRequest struct {
	New        bool
	Session    string
	ExpectedID string
}

func (r AttachRequest) Validate() error {
	if r.New && (r.Session != "" || r.ExpectedID != "") {
		return errors.New("--new and --session are mutually exclusive")
	}
	if r.Session != "" && sessionNumber(r.Session) == 0 {
		return fmt.Errorf("invalid Claude session %q: use cspace-claude or cspace-claude-N (N >= 2)", r.Session)
	}
	if r.ExpectedID != "" && r.Session == "" {
		return errors.New("a session identity requires a session name")
	}
	return nil
}

// Session describes a running managed Claude process, not one attached client.
// ID changes when a name is reused. State is a local hook-file sample, so status
// updates need no exec into the guest after discovery.
type Session struct {
	Name    string
	ID      string
	Created time.Time
	State   InteractiveState

	// The tmux target and generation are kept separately from the label. A
	// selected name must never quietly attach to its later replacement.
	target     string
	generation string
	stateID    string
}

func (s Session) Label() string { return fmt.Sprintf("Claude %d", sessionNumber(s.Name)) }

var managedSession = regexp.MustCompile(`^cspace-claude-([2-9]|[1-9][0-9]+)$`)
var tmuxSessionID = regexp.MustCompile(`^\$[0-9]+$`)
var stateIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var sessionGenerationPattern = regexp.MustCompile(`^[1-9][0-9]*/[1-9][0-9]*/\$[0-9]+/[1-9][0-9]*/([0-9a-f]{32})?$`)

func sessionNumber(name string) int {
	if name == SessionClaude {
		return 1
	}
	m := managedSession.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// All fields are supported by the image's tmux 3.3a. Server start time and PID
// prevent a reused $0 after a server restart from looking like the same session.
// New sessions additionally carry a random token, also used for their state file.
const sessionGenerationFormat = "#{pid}/#{start_time}/#{session_id}/#{session_created}/#{@cspace_id}"
const sessionListFormat = "#{session_name}\t#{session_id}\t#{session_created}\t#{pid}\t#{start_time}\t#{@cspace_id}"

func parseSessions(out string) ([]Session, error) {
	var sessions []Session
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if sessionNumber(fields[0]) == 0 {
			continue // other tmux sessions, including cspace-shell, are not Claude
		}
		if len(fields) != 6 || !tmuxSessionID.MatchString(fields[1]) {
			return nil, fmt.Errorf("invalid tmux session record for %s", fields[0])
		}
		created, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || created <= 0 {
			return nil, fmt.Errorf("invalid tmux creation time for %s", fields[0])
		}
		for _, v := range fields[3:5] {
			if n, err := strconv.ParseInt(v, 10, 64); err != nil || n <= 0 {
				return nil, fmt.Errorf("invalid tmux server identity for %s", fields[0])
			}
		}
		if fields[5] != "" && !stateIDPattern.MatchString(fields[5]) {
			return nil, fmt.Errorf("invalid cspace session identity for %s", fields[0])
		}
		generation := strings.Join([]string{fields[3], fields[4], fields[1], fields[2], fields[5]}, "/")
		sessions = append(sessions, Session{Name: fields[0], ID: generation,
			Created: time.Unix(created, 0), target: fields[1], generation: generation, stateID: fields[5]})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessionNumber(sessions[i].Name) < sessionNumber(sessions[j].Name) })
	return sessions, nil
}

// Sessions lists existing Claude sessions. Missing tmux/server means no managed
// sessions; an unreachable sandbox or other failed query is an error, not empty.
func (t *Tmux) Sessions(ctx context.Context, container string) ([]Session, error) {
	present, err := t.Present(ctx, container)
	if err != nil || !present {
		return nil, err
	}
	out, code, err := t.Exec.Exec(ctx, container, []string{"tmux", "list-sessions", "-F", sessionListFormat})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if clientAlreadyGone(strings.TrimSpace(out)) {
			return nil, nil
		}
		return nil, fmt.Errorf("list Claude sessions in %s: exit %d: %s", container, code, strings.TrimSpace(out))
	}
	return parseSessions(out)
}

func (c *Client) Sessions(ctx context.Context, project, sandbox string) ([]Session, error) {
	sessions, err := c.tmux.Sessions(ctx, containerName(project, sandbox))
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		sessions[i].State = c.SessionState(project, sandbox, sessions[i])
	}
	return sessions, nil
}

// SessionState reads only this incarnation's hook file. Legacy Claude 1 has no
// token and keeps its existing agent-state.json convention. Unknown numbered
// sessions must not borrow that file and claim another Claude's status.
func (c *Client) SessionState(project, sandbox string, session Session) InteractiveState {
	if c.home == "" {
		return InteractiveState{}
	}
	if stateIDPattern.MatchString(session.stateID) {
		return ReadInteractiveState(filepath.Join(SessionDir(c.home, project, sandbox), "interactive", session.stateID+".json"))
	}
	if session.Name == SessionClaude {
		state := c.InteractiveState(project, sandbox)
		// A record left by a previous Claude must not decorate its replacement
		// before the replacement's first hook has fired.
		if at, err := time.Parse(time.RFC3339, state.At); err == nil && at.Before(session.Created) {
			return InteractiveState{}
		}
		return state
	}
	return InteractiveState{}
}

type PreparedAttach struct {
	Spec       AttachSpec
	Attachment *Attachment
	Session    Session
}

var ErrSessionGone = errors.New("requested Claude session no longer exists")
var ErrTmuxRequired = errors.New("multiple Claude sessions require tmux; rebuild the sandbox image and recreate the sandbox")

// PrepareClaudeAttach creates a numbered session or resolves an existing one,
// then transfers the sandbox's existing attach lock to the client tracker.
// Callers start Spec promptly and always close Attachment, including on spawn
// failure. A zero request creates or joins the default Claude session, while
// still returning its exact identity before the attaching client starts.
func PrepareClaudeAttach(ctx context.Context, tm *Tmux, home, project, sandbox, container string, req AttachRequest) (PreparedAttach, error) {
	if err := req.Validate(); err != nil {
		return PreparedAttach{}, err
	}
	if home == "" {
		return PreparedAttach{}, ErrNoHome
	}
	present, err := tm.Present(ctx, container)
	if err != nil {
		return PreparedAttach{}, err
	}
	if !present {
		return PreparedAttach{}, ErrTmuxRequired
	}
	dir := ControlPlaneDir(home, project, sandbox)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PreparedAttach{}, fmt.Errorf("create attach directory: %w: %w", ErrBookkeepingUnavailable, err)
	}
	lock, err := lockAttach(ctx, dir, attachLockWait(tm))
	if err != nil {
		return PreparedAttach{}, err
	}
	att := &Attachment{tmux: tm, container: container, dir: dir, lock: lock, trackDone: make(chan struct{})}
	transferred := false
	defer func() {
		if !transferred {
			att.releaseLock()
		}
	}()
	sessions, err := tm.Sessions(ctx, container)
	if err != nil {
		return PreparedAttach{}, err
	}
	var session Session
	if req.New {
		// Reserve Claude 1 for the unchanged default attach command. Numbering
		// advances above the live set so concurrent cspace creators cannot join
		// one another's processes; new-session deliberately has no -A.
		next := 2
		for _, s := range sessions {
			if n := sessionNumber(s.Name); n >= next {
				if n == math.MaxInt {
					return PreparedAttach{}, errors.New("managed Claude session numbers exhausted")
				}
				next = n + 1
			}
		}
		name := fmt.Sprintf("%s-%d", SessionClaude, next)
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return PreparedAttach{}, fmt.Errorf("allocate session identity: %w", err)
		}
		id := hex.EncodeToString(token[:])
		cmd := []string{"tmux", "-u", "-f", TmuxConf, "new-session", "-d", "-s", name, "-c", Workspace,
			"env", "CSPACE_AGENT_STATE_FILE=/sessions/interactive/" + id + ".json"}
		// tmux owns the child's TERM (tmux-256color). Preserve the host's
		// truecolor capability without replacing that inner terminal type.
		if value := TerminalEnv(os.Getenv("TERM"), os.Getenv("COLORTERM"))["COLORTERM"]; value != "" {
			cmd = append(cmd, "COLORTERM="+value)
		}
		cmd = append(cmd, "claude", "--dangerously-skip-permissions",
			";", "set-option", "-t", "="+name, "@cspace_id", id,
			";", "display-message", "-p", "-t", "="+name, sessionListFormat)
		out, code, err := tm.Exec.Exec(ctx, container, cmd)
		if err != nil {
			return PreparedAttach{}, err
		}
		if code != 0 {
			return PreparedAttach{}, fmt.Errorf("create Claude session %s: exit %d: %s", name, code, strings.TrimSpace(out))
		}
		created, err := parseSessions(out)
		if err != nil {
			return PreparedAttach{}, err
		}
		if len(created) != 1 || created[0].Name != name || created[0].stateID != id {
			return PreparedAttach{}, fmt.Errorf("create Claude session %s: session ended before it could be attached", name)
		}
		session = created[0]
	} else {
		name := req.Session
		if name == "" {
			name = SessionClaude
		}
		for _, s := range sessions {
			if s.Name == name {
				session = s
				break
			}
		}
		if session.Name == "" && req.Session == "" {
			// The default UI action retains create-or-join semantics, but
			// creation happens before launching the client so the pane can be
			// pinned to one incarnation. Keep Claude 1's legacy hook file.
			cmd := []string{"tmux", "-u", "-f", TmuxConf, "new-session", "-d", "-s", SessionClaude, "-c", Workspace, "env"}
			if value := TerminalEnv(os.Getenv("TERM"), os.Getenv("COLORTERM"))["COLORTERM"]; value != "" {
				cmd = append(cmd, "COLORTERM="+value)
			}
			cmd = append(cmd, "claude", "--dangerously-skip-permissions", ";", "display-message", "-p", "-t", "="+SessionClaude, sessionListFormat)
			out, code, createErr := tm.Exec.Exec(ctx, container, cmd)
			if createErr != nil {
				return PreparedAttach{}, createErr
			}
			if code != 0 {
				return PreparedAttach{}, fmt.Errorf("create default Claude session: exit %d: %s", code, strings.TrimSpace(out))
			}
			created, parseErr := parseSessions(out)
			if parseErr != nil {
				return PreparedAttach{}, parseErr
			}
			if len(created) != 1 || created[0].Name != SessionClaude {
				return PreparedAttach{}, errors.New("default Claude session ended before it could be attached")
			}
			session = created[0]
		}
		if session.Name == "" || (req.ExpectedID != "" && req.ExpectedID != session.ID) {
			return PreparedAttach{}, fmt.Errorf("%w: %s", ErrSessionGone, req.Session)
		}
	}
	att.session = session.Name
	tracked, err := beginAttachLocked(ctx, att)
	if err != nil {
		return PreparedAttach{}, err
	}
	transferred = true
	spec := ClaudeAttach(container, true)
	spec.Session = session.Name
	spec.ExistingSession = &session
	return PreparedAttach{Spec: spec, Attachment: tracked, Session: session}, nil
}
