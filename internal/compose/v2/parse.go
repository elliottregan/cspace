package v2

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
)

func Parse(ctx context.Context, path string) (*Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// First pass: load with all profiles activated to check for profile usage.
	// This allows validateSubset to see if any service uses profiles.
	optsPrecheck, err := cli.NewProjectOptions(
		[]string{abs},
		cli.WithName(filepath.Base(filepath.Dir(abs))),
		cli.WithResolvedPaths(true),
		cli.WithProfiles([]string{"*"}), // Activate all profiles for validation check
	)
	if err != nil {
		return nil, fmt.Errorf("compose options: %w", err)
	}
	cgProjPrecheck, err := optsPrecheck.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("compose load: %w", err)
	}
	if err := validateSubset(cgProjPrecheck); err != nil {
		return nil, err
	}

	// Second pass: load normally (without forcing all profiles) for actual use
	opts, err := cli.NewProjectOptions(
		[]string{abs},
		cli.WithName(filepath.Base(filepath.Dir(abs))),
		cli.WithResolvedPaths(true),
	)
	if err != nil {
		return nil, fmt.Errorf("compose options: %w", err)
	}
	cgProj, err := opts.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("compose load: %w", err)
	}
	return translate(cgProj, abs)
}

func translate(cg *types.Project, srcPath string) (*Project, error) {
	p := &Project{
		Name:         cg.Name,
		Services:     map[string]*Service{},
		NamedVolumes: map[string]*NamedVolume{},
		SourcePath:   srcPath,
	}
	for name, vol := range cg.Volumes {
		p.NamedVolumes[name] = &NamedVolume{External: bool(vol.External), Name: vol.Name}
	}
	for name, svc := range cg.Services {
		s := &Service{
			Name:        name,
			Image:       svc.Image,
			Command:     []string(svc.Command),
			Entrypoint:  []string(svc.Entrypoint),
			Environment: map[string]string{},
			Restart:     svc.Restart,
			WorkingDir:  svc.WorkingDir,
			User:        svc.User,
			TTY:         svc.Tty,
			StdinOpen:   svc.StdinOpen,
			Init:        svc.Init != nil && *svc.Init,
		}
		for k, v := range svc.Environment {
			if v != nil {
				s.Environment[k] = *v
			}
		}
		for _, ef := range svc.EnvFiles {
			s.EnvFiles = append(s.EnvFiles, ef.Path)
		}
		if svc.Build != nil {
			s.Build = &Build{
				Context:    svc.Build.Context,
				Dockerfile: svc.Build.Dockerfile,
				Args:       toStringMap(svc.Build.Args),
				Target:     svc.Build.Target,
			}
		}
		for _, p := range svc.Ports {
			s.Ports = append(s.Ports, Port{
				Container: int(p.Target),
				Host:      atoiSafe(p.Published),
				Protocol:  p.Protocol,
			})
		}
		for _, v := range svc.Volumes {
			s.Volumes = append(s.Volumes, Volume{
				Type:     v.Type,
				Source:   v.Source,
				Target:   v.Target,
				ReadOnly: v.ReadOnly,
			})
		}
		for depName, dep := range svc.DependsOn {
			s.DependsOn = append(s.DependsOn, Dependency{
				Name: depName, Condition: dep.Condition,
			})
		}
		if svc.HealthCheck != nil && !boolDeref(svc.HealthCheck.Disable) {
			s.Healthcheck = &Healthcheck{
				Test:        []string(svc.HealthCheck.Test),
				Interval:    durationOr(svc.HealthCheck.Interval, 30*time.Second),
				Timeout:     durationOr(svc.HealthCheck.Timeout, 30*time.Second),
				Retries:     uintOr(svc.HealthCheck.Retries, 3),
				StartPeriod: durationOr(svc.HealthCheck.StartPeriod, 0),
			}
		}
		p.Services[name] = s
	}
	return p, nil
}

func toStringMap(m types.MappingWithEquals) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func durationOr(d *types.Duration, def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	return time.Duration(*d)
}

func uintOr(i *uint64, def int) int {
	if i == nil {
		return def
	}
	return int(*i)
}

func boolDeref(b bool) bool { return b }

func atoiSafe(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
