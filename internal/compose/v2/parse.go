package v2

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
)

// composeProjectName derives a name acceptable to compose-go from the
// compose file's parent directory. Compose-go requires lowercase
// alphanumeric + hyphen/underscore, leading with letter or digit.
// A bare directory like ".devcontainer" would otherwise reject.
func composeProjectName(absPath string) string {
	raw := filepath.Base(filepath.Dir(absPath))
	raw = strings.ToLower(raw)
	// Strip any non-conforming leading runes (dots, hyphens, etc.).
	for len(raw) > 0 {
		r := raw[0]
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			break
		}
		raw = raw[1:]
	}
	if raw == "" {
		return "cspace"
	}
	// Replace any remaining illegal characters with '-'.
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func Parse(ctx context.Context, path string) (*Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	projectName := composeProjectName(abs)
	// First pass: load with all profiles activated to check for profile usage.
	// This allows validateSubset to see if any service uses profiles.
	optsPrecheck, err := cli.NewProjectOptions(
		[]string{abs},
		cli.WithName(projectName),
		cli.WithResolvedPaths(true),
		cli.WithProfiles([]string{"*"}), // Activate all profiles for validation check
	)
	if err != nil {
		return nil, fmt.Errorf("compose options: %w", err)
	}
	cgProjPrecheck, err := optsPrecheck.LoadProject(ctx)
	if err != nil {
		return nil, wrapLoadError(err)
	}
	warnings, err := validateSubset(cgProjPrecheck)
	if err != nil {
		return nil, err
	}

	// Second pass: load normally (without forcing all profiles) for actual use
	opts, err := cli.NewProjectOptions(
		[]string{abs},
		cli.WithName(projectName),
		cli.WithResolvedPaths(true),
	)
	if err != nil {
		return nil, fmt.Errorf("compose options: %w", err)
	}
	cgProj, err := opts.LoadProject(ctx)
	if err != nil {
		return nil, wrapLoadError(err)
	}
	proj, err := translate(cgProj, abs)
	if err != nil {
		return nil, err
	}
	proj.Warnings = warnings
	return proj, nil
}

// wrapLoadError attaches a clearer remediation hint for missing env_file
// references. compose-go errors with "env file <path> not found:" when the
// short-form `env_file: - <path>` points to a path that doesn't exist;
// rather than relax compose-spec strictness, surface a one-line fix.
func wrapLoadError(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "env file ") && strings.Contains(msg, " not found") {
		return fmt.Errorf("compose load: %w\n\nhint: your compose declares an env_file: pointing at a path that doesn't exist. Create the file (an empty file is fine: `touch <path>`), or remove the env_file: entry from the compose service", err)
	}
	return fmt.Errorf("compose load: %w", err)
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
			// Long-form `type: tmpfs` volumes go through the tmpfs path
			// rather than the bind/named-volume path. tmpfs option is
			// captured here when present.
			if v.Type == "tmpfs" {
				size := 0
				if v.Tmpfs != nil {
					size = int(v.Tmpfs.Size / (1024 * 1024))
				}
				s.Tmpfs = append(s.Tmpfs, TmpfsMount{
					Target:  v.Target,
					SizeMiB: size,
				})
				continue
			}
			s.Volumes = append(s.Volumes, Volume{
				Type:     v.Type,
				Source:   v.Source,
				Target:   v.Target,
				ReadOnly: v.ReadOnly,
			})
		}
		// Short-form `tmpfs:` directive at service level (string or list).
		// compose-go presents this as svc.Tmpfs StringList; size isn't
		// settable in short form, so use the adapter's default.
		for _, t := range svc.Tmpfs {
			s.Tmpfs = append(s.Tmpfs, TmpfsMount{Target: t})
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
