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
	DependsOn   []Dependency
	Healthcheck *Healthcheck
	Restart     string
	WorkingDir  string
	User        string
	TTY         bool
	StdinOpen   bool
	Init        bool
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
