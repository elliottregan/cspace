// Package v2 parses docker-compose YAML files into a cspace-internal
// model representing only the subset cspace supports. Anything outside
// the subset is hard-rejected by Validate (see subset.go).
package v2

import "time"

type Project struct {
	Name         string
	Services     map[string]*Service
	NamedVolumes map[string]*NamedVolume
	SourcePath   string
	// Warnings collected during parse/validate (e.g. compose fields stripped
	// because Apple Container can't honor them). Callers should surface
	// these on stderr.
	Warnings []string
}

type Service struct {
	Name        string
	Image       string
	Build       *Build
	Command     []string
	Entrypoint  []string
	Environment map[string]string
	EnvFiles    []string
	Ports       []Port
	Volumes     []Volume
	Tmpfs       []TmpfsMount
	DependsOn   []Dependency
	Healthcheck *Healthcheck
	Restart     string
	WorkingDir  string
	User        string
	TTY         bool
	StdinOpen   bool
	Init        bool
}

// TmpfsMount is a RAM-backed in-microVM mount declared via compose's
// tmpfs: directive. Use for build artifacts (node_modules, .next, etc.)
// that should not pollute the host bind mount.
type TmpfsMount struct {
	Target  string
	SizeMiB int // 0 = adapter default
}

type Build struct {
	Context    string
	Dockerfile string
	Args       map[string]string
	Target     string
}

type Port struct {
	Container int
	Host      int    // 0 if unspecified
	Protocol  string // tcp | udp
}

type Volume struct {
	Type     string // bind | volume
	Source   string
	Target   string
	ReadOnly bool
}

type Dependency struct {
	Name      string
	Condition string // service_started | service_healthy | service_completed_successfully
}

type Healthcheck struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	Retries     int
	StartPeriod time.Duration
}

type NamedVolume struct {
	External bool
	Name     string // for external: true with explicit name
}
