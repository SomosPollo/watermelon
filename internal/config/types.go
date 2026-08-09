package config

// Config represents .watermelon.toml
type Config struct {
	VM        VMConfig            `toml:"vm" json:"vm"`
	Network   NetworkConfig       `toml:"network" json:"network"`
	Provision ProvisionConfig     `toml:"provision" json:"provision"`
	Tools     map[string][]string `toml:"tools" json:"tools"`
	Mounts    map[string]Mount    `toml:"mounts" json:"mounts"`
	Ports     PortsConfig         `toml:"ports" json:"ports"`
	Resources ResourcesConfig     `toml:"resources" json:"resources"`
	Security  SecurityConfig      `toml:"security" json:"security"`
	IDE       IDEConfig           `toml:"ide" json:"ide"`
}

type VMConfig struct {
	Image string `toml:"image" json:"image"`
}

type NetworkConfig struct {
	Allow   []string            `toml:"allow" json:"allow"`
	Process map[string][]string `toml:"process" json:"process"`
}

type ProvisionConfig struct {
	Npm   []string `toml:"npm" json:"npm"`
	Pip   []string `toml:"pip" json:"pip"`
	Cargo []string `toml:"cargo" json:"cargo"`
	Go    []string `toml:"go" json:"go"`
	Gem   []string `toml:"gem" json:"gem"`
}

type Mount struct {
	Target string `toml:"target" json:"target"`
	Mode   string `toml:"mode" json:"mode"` // "ro" or "rw", default "ro"
}

type PortsConfig struct {
	Forward []int `toml:"forward" json:"forward"`
}

type ResourcesConfig struct {
	Memory string `toml:"memory" json:"memory"`
	CPUs   int    `toml:"cpus" json:"cpus"`
	Disk   string `toml:"disk" json:"disk"`
}

type SecurityConfig struct {
	Enforcement string `toml:"enforcement" json:"enforcement"`
}

type IDEConfig struct {
	Command string `toml:"command" json:"command"`
}

// NewConfig returns a Config with default values
func NewConfig() *Config {
	return &Config{
		VM: VMConfig{
			Image: "ubuntu-22.04",
		},
		Network: NetworkConfig{
			Allow:   []string{},
			Process: map[string][]string{},
		},
		Provision: ProvisionConfig{
			Npm:   []string{},
			Pip:   []string{},
			Cargo: []string{},
			Go:    []string{},
			Gem:   []string{},
		},
		Tools:  map[string][]string{},
		Mounts: map[string]Mount{},
		Ports: PortsConfig{
			Forward: []int{},
		},
		Resources: ResourcesConfig{
			Memory: "2GB",
			CPUs:   1,
			Disk:   "10GB",
		},
		Security: SecurityConfig{
			Enforcement: EnforcementFail,
		},
		IDE: IDEConfig{
			Command: "code",
		},
	}
}
