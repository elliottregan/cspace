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
)

type Entry struct {
	Project    string    `json:"-"`
	Name       string    `json:"-"`
	ControlURL string    `json:"control_url"`
	Token      string    `json:"token,omitempty"`
	IP         string    `json:"ip,omitempty"`
	StartedAt  time.Time `json:"started_at"`
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

func (r *Registry) Register(e Entry) error {
	m, err := r.load()
	if err != nil {
		return err
	}
	m[key(e.Project, e.Name)] = e
	return r.save(m)
}

func (r *Registry) Unregister(project, name string) error {
	m, err := r.load()
	if err != nil {
		return err
	}
	delete(m, key(project, name))
	return r.save(m)
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
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
