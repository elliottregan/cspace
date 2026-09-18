package control

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elliottregan/cspace/internal/registry"
)

func TestParseListeningPorts(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []int
	}{
		{
			name: "header is skipped and the bind address ignored",
			// The entrypoint's PREROUTING DNAT rewrites inbound traffic to
			// 127.0.0.1, so a loopback-only listener is still reachable from
			// outside the microVM — the bind address must not filter.
			out: "State  Recv-Q Send-Q Local Address:Port  Peer Address:Port\n" +
				"LISTEN 0      511          0.0.0.0:5173         0.0.0.0:*\n" +
				"LISTEN 0      4096       127.0.0.1:6201         0.0.0.0:*\n",
			want: []int{5173, 6201},
		},
		{
			name: "IPv6 brackets parse and duplicates collapse",
			out: "LISTEN 0 511    [::]:3000    [::]:*\n" +
				"LISTEN 0 511 0.0.0.0:3000 0.0.0.0:*\n",
			want: []int{3000},
		},
		{
			name: "non-LISTEN and short lines are ignored",
			out:  "ESTAB 0 0 10.0.0.1:5173 10.0.0.2:5555\nrubbish\n\n",
			want: nil,
		},
		{
			name: "output is sorted ascending",
			out: "LISTEN 0 511 *:8080 *:*\n" +
				"LISTEN 0 511 *:3000 *:*\n",
			want: []int{3000, 8080},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseListeningPorts(tc.out); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseListeningPorts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPortLabelsFrom(t *testing.T) {
	writeClone := func(t *testing.T, devcontainerJSON, cspaceJSON string) string {
		t.Helper()
		dir := t.TempDir()
		if devcontainerJSON != "" {
			if err := os.MkdirAll(filepath.Join(dir, ".devcontainer"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".devcontainer", "devcontainer.json"), []byte(devcontainerJSON), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if cspaceJSON != "" {
			if err := os.WriteFile(filepath.Join(dir, ".cspace.json"), []byte(cspaceJSON), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	cases := []struct {
		name         string
		devcontainer string
		cspace       string
		want         map[int]string
	}{
		{
			name: "devcontainer portsAttributes wins, JSONC comments and all",
			devcontainer: `{
				// the dev server
				"name": "demo",
				"portsAttributes": {"5173": {"label": "dev"}, "4173": {"label": "preview"}}
			}`,
			cspace: `{"container":{"ports":{"9999":"ignored"}}}`,
			want:   map[int]string{5173: "dev", 4173: "preview"},
		},
		{
			name:         "entries without a label are dropped",
			devcontainer: `{"portsAttributes": {"5173": {"label": "dev"}, "24678": {"onAutoForward": "silent"}}}`,
			want:         map[int]string{5173: "dev"},
		},
		{
			name:   "falls back to .cspace.json container.ports",
			cspace: `{"container":{"ports":{"3000":"api","5173":"dev"}}}`,
			want:   map[int]string{3000: "api", 5173: "dev"},
		},
		{
			name:         "an empty portsAttributes still falls back",
			devcontainer: `{"portsAttributes": {}}`,
			cspace:       `{"container":{"ports":{"3000":"api"}}}`,
			want:         map[int]string{3000: "api"},
		},
		{
			name: "no sources at all yields no labels",
			want: map[int]string{},
		},
		{
			name:   "an empty ports object yields no labels",
			cspace: `{"container":{"ports":{}}}`,
			want:   map[int]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := portLabelsFrom(writeClone(t, tc.devcontainer, tc.cspace))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("portLabelsFrom = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCuratePorts(t *testing.T) {
	cases := []struct {
		name      string
		listening []int
		labels    map[int]string
		want      []Port
	}{
		{
			// The curation gate: labels present means the project told us what
			// it cares about, so everything unlabeled is noise.
			name:      "labels hide unlabeled ports",
			listening: []int{3000, 5173, 24678},
			labels:    map[int]string{5173: "dev"},
			want:      []Port{{Port: 5173, Label: "dev"}},
		},
		{
			// No labels is no signal, so show everything rather than nothing.
			name:      "no labels shows every listener",
			listening: []int{3000, 5173},
			labels:    map[int]string{},
			want:      []Port{{Port: 3000}, {Port: 5173}},
		},
		{
			// 6201 is the supervisor control port, 53 the dnsmasq forwarder:
			// cspace plumbing, never a user's dev server, even when labeled.
			name:      "cspace-internal ports never show",
			listening: []int{53, 5173, 6201},
			labels:    map[int]string{6201: "control", 5173: "dev"},
			want:      []Port{{Port: 5173, Label: "dev"}},
		},
		{
			name:      "output is sorted ascending",
			listening: []int{8080, 3000},
			labels:    map[int]string{},
			want:      []Port{{Port: 3000}, {Port: 8080}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := curatePorts(tc.listening, tc.labels)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("curatePorts = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPortURL(t *testing.T) {
	cases := []struct {
		name     string
		resolver bool
		want     string
	}{
		{"resolver installed uses the project-qualified name", true, "http://mercury.alpha.cspace.test:5173/"},
		{"no resolver falls back to the sandbox IP", false, "http://10.0.0.1:5173/"},
	}
	for _, tc := range cases {
		if got := portURL("Alpha", "Mercury", "10.0.0.1", 5173, tc.resolver); got != tc.want {
			t.Errorf("%s: portURL = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClientPortsEndToEnd(t *testing.T) {
	home := t.TempDir()
	clone := CloneDir(home, "alpha", "mercury")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, ".cspace.json"),
		[]byte(`{"container":{"ports":{"5173":"dev"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	if err := reg.Register(registry.Entry{
		Project: "alpha", Name: "mercury", IP: "10.0.0.1", State: "ready",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	fc := &fakeContainers{execOut: "State Recv-Q Send-Q Local Address:Port Peer Address:Port\n" +
		"LISTEN 0 511    0.0.0.0:5173  0.0.0.0:*\n" +
		"LISTEN 0 511    0.0.0.0:24678 0.0.0.0:*\n" +
		"LISTEN 0 4096 127.0.0.1:6201  0.0.0.0:*\n"}
	c := New(Options{
		Containers:        fc,
		Entries:           reg,
		Home:              home,
		ResolverInstalled: func() bool { return true },
	})

	got, err := c.Ports(context.Background(), "alpha", "mercury")
	if err != nil {
		t.Fatalf("Ports: %v", err)
	}
	want := []Port{{Port: 5173, Label: "dev", URL: "http://mercury.alpha.cspace.test:5173/"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ports = %+v, want %+v", got, want)
	}
	// The listener enumeration must run inside the sandbox's own container.
	wantExec := execCall{name: "cspace-alpha-mercury", cmd: []string{"ss", "-tln"}}
	if len(fc.execCalls) != 1 || !reflect.DeepEqual(fc.execCalls[0], wantExec) {
		t.Errorf("exec calls = %+v, want exactly %+v", fc.execCalls, wantExec)
	}
}

func TestClientPortsSurfacesExecFailure(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	if err := reg.Register(registry.Entry{Project: "alpha", Name: "mercury", State: "ready"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c := New(Options{
		Containers: &fakeContainers{execExit: 127, execStderr: "ss: command not found"},
		Entries:    reg,
		Home:       t.TempDir(),
	})
	if _, err := c.Ports(context.Background(), "alpha", "mercury"); err == nil {
		t.Error("a non-zero ss exit must surface as an error")
	}
}
