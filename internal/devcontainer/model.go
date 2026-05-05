// Package devcontainer reads .devcontainer/devcontainer.json files and
// validates them against cspace's supported subset of the spec.
package devcontainer

type Config struct {
	Name              string              `json:"name,omitempty"`
	Image             string              `json:"image,omitempty"`
	DockerFile        string              `json:"dockerFile,omitempty"`
	Build             *BuildConfig        `json:"build,omitempty"`
	DockerComposeFile StringOrSlice       `json:"dockerComposeFile,omitempty"`
	Service           string              `json:"service,omitempty"`
	RunServices       []string            `json:"runServices,omitempty"`
	WorkspaceFolderRaw string             `json:"workspaceFolder,omitempty"`
	ContainerEnv      map[string]string   `json:"containerEnv,omitempty"`
	Mounts            []Mount             `json:"mounts,omitempty"`
	ForwardPorts      []ForwardPort       `json:"forwardPorts,omitempty"`
	PortsAttributes   map[string]PortAttr `json:"portsAttributes,omitempty"`
	PostCreateCommand StringOrSlice       `json:"postCreateCommand,omitempty"`
	PostStartCommand  StringOrSlice       `json:"postStartCommand,omitempty"`
	RemoteUserRaw     string              `json:"remoteUser,omitempty"`
	Features          map[string]any      `json:"features,omitempty"`
	Customizations    Customizations      `json:"customizations,omitempty"`

	// SourcePath is set by Load to the file path the config was parsed from.
	SourcePath string `json:"-"`
	// Unknown captures fields not in the supported set, for hard-reject validation.
	Unknown map[string]any `json:"-"`
}

type BuildConfig struct {
	Context    string            `json:"context,omitempty"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Target     string            `json:"target,omitempty"`
}

type Mount struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // bind | volume
}

type ForwardPort struct {
	Port int    `json:"port"`
	Host string `json:"host,omitempty"` // localhost | all (default localhost)
}

type PortAttr struct {
	Label         string `json:"label,omitempty"`
	OnAutoForward string `json:"onAutoForward,omitempty"`
}

type Customizations struct {
	Cspace CspaceCustomizations `json:"cspace,omitempty"`
}

type CspaceCustomizations struct {
	ExtractCredentials []ExtractCredential `json:"extractCredentials,omitempty"`
	Resources          *Resources          `json:"resources,omitempty"`
	Plugins            []string            `json:"plugins,omitempty"`
	FirewallDomains    []string            `json:"firewallDomains,omitempty"`
}

type ExtractCredential struct {
	From string   `json:"from"`
	Exec []string `json:"exec"`
	Env  string   `json:"env"`
	Trim *bool    `json:"trim,omitempty"` // default true
}

type Resources struct {
	CPUs      int `json:"cpus,omitempty"`
	MemoryMiB int `json:"memoryMiB,omitempty"`
}

// StringOrSlice represents a JSON value that may be a string or []string.
// UnmarshalJSON is implemented in a separate file (Task 3).
type StringOrSlice []string

func (c Config) WorkspaceFolder() string {
	if c.WorkspaceFolderRaw == "" {
		return "/workspace"
	}
	return c.WorkspaceFolderRaw
}

func (c Config) RemoteUser() string {
	if c.RemoteUserRaw == "" {
		return "dev"
	}
	return c.RemoteUserRaw
}

func (c Config) ShouldTrim(ec ExtractCredential) bool {
	if ec.Trim == nil {
		return true
	}
	return *ec.Trim
}
