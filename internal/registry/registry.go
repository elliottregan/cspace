// Package registry tracks active cspace sandboxes and how to reach them.
// File format: ~/.cspace/sandbox-registry.json
//
//	{
//	  "myproj:coordinator": {"control_url":"...","token":"...","ip":"...","started_at":"..."},
//	  ...
//	}
//
// Concurrency: writes go through a tmp-file + atomic rename. The file is
// re-read on every operation; we never hold an in-memory snapshot across calls.
package registry

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type Entry struct {
	Project          string    `json:"-"`
	Name             string    `json:"-"`
	ControlURL       string    `json:"control_url"`
	Token            string    `json:"token,omitempty"`
	IP               string    `json:"ip,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	BrowserContainer string    `json:"browser_container,omitempty"`
	// State is the entry-internal lifecycle: "starting" while cspace up is
	// still booting the sandbox, "ready" once /health responded 200. Empty
	// State on legacy entries (written before this field existed) is treated
	// as "ready" by callers — those sandboxes were already past boot when
	// they were registered under the old single-write flow.
	State string `json:"state,omitempty"`
}

type Registry struct {
	Path string
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cspace", "sandbox-registry.json"), nil
}

func (r *Registry) load() (map[string]Entry, error) {
	data, err := os.ReadFile(r.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Entry{}, nil
		}
		return nil, err
	}
	m := map[string]Entry{}
	if len(data) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return m, nil
}

func (r *Registry) save(m map[string]Entry) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return err
	}
	tmp := r.Path + ".tmp"
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.Path)
}

func key(project, name string) string { return project + ":" + name }

// withLock acquires an exclusive flock on r.Path + ".lock" for the duration of fn.
// The lock file is created on demand. The lock is released when fn returns.
//
// This serializes read-modify-write operations across goroutines AND processes,
// preventing the lost-write race where two concurrent Register/Unregister calls
// both load the same snapshot and the later save clobbers the earlier one.
//
// Lookup and List are read-only and intentionally NOT wrapped — os.ReadFile is
// kernel-atomic, and atomic-rename writes prevent torn reads.
func (r *Registry) withLock(fn func() error) error {
	lockPath := r.Path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()
	return fn()
}

func (r *Registry) Register(e Entry) error {
	return r.withLock(func() error {
		m, err := r.load()
		if err != nil {
			return err
		}
		m[key(e.Project, e.Name)] = e
		return r.save(m)
	})
}

// MarkReady transitions an existing entry's State to "ready". No-op if the
// entry is missing — callers should not treat that as an error since the
// most common reason for it is a racing Unregister (e.g. cspace down ran
// while cspace up was still in its /health-poll window).
func (r *Registry) MarkReady(project, name string) error {
	return r.withLock(func() error {
		m, err := r.load()
		if err != nil {
			return err
		}
		e, ok := m[key(project, name)]
		if !ok {
			return nil
		}
		e.State = "ready"
		m[key(project, name)] = e
		return r.save(m)
	})
}

func (r *Registry) Unregister(project, name string) error {
	return r.withLock(func() error {
		m, err := r.load()
		if err != nil {
			return err
		}
		delete(m, key(project, name))
		return r.save(m)
	})
}

func (r *Registry) Lookup(project, name string) (Entry, error) {
	m, err := r.load()
	if err != nil {
		return Entry{}, err
	}
	e, ok := m[key(project, name)]
	if !ok {
		return Entry{}, fmt.Errorf("sandbox %s:%s not registered", project, name)
	}
	e.Project, e.Name = project, name
	return e, nil
}

func (r *Registry) List() ([]Entry, error) {
	m, err := r.load()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(m))
	for k, v := range m {
		var project, name string
		for i := 0; i < len(k); i++ {
			if k[i] == ':' {
				project, name = k[:i], k[i+1:]
				break
			}
		}
		v.Project, v.Name = project, name
		out = append(out, v)
	}
	return out, nil
}

// FreePort asks the kernel for an unused TCP port on 127.0.0.1.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
