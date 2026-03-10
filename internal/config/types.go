package config

// Config represents .watermelon.toml
type Config struct {
	VM        VMConfig            `toml:"vm"`
	Network   NetworkConfig       `toml:"network"`
	Provision ProvisionConfig     `toml:"provision"`
	Tools     map[string][]string `toml:"tools"`
	Mounts    map[string]Mount    `toml:"mounts"`
	Ports     PortsConfig         `toml:"ports"`
	Resources ResourcesConfig     `toml:"resources"`
	Security  SecurityConfig      `toml:"security"`
	IDE       IDEConfig           `toml:"ide"`
}

type VMConfig struct {
	Image string `toml:"image"`
}

type NetworkConfig struct {
	Allow   []string            `toml:"allow"`
	Process map[string][]string `toml:"process"`
}

type ProvisionConfig struct {
	Npm   []string `toml:"npm"`
	Pip   []string `toml:"pip"`
	Cargo []string `toml:"cargo"`
	Go    []string `toml:"go"`
	Gem   []string `toml:"gem"`
}

type Mount struct {
	Target string `toml:"target"`
	Mode   string `toml:"mode"` // "ro" or "rw", default "ro"
}

type PortsConfig struct {
	Forward []int `toml:"forward"`
}

type ResourcesConfig struct {
	Memory string `toml:"memory"`
	CPUs   int    `toml:"cpus"`
	Disk   string `toml:"disk"`
}

type SecurityConfig struct {
	Enforcement string `toml:"enforcement"`
}

type IDEConfig struct {
	Command string `toml:"command"`
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
		Tools: map[string][]string{},
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
			Enforcement: "log",
		},
		IDE: IDEConfig{
			Command: "code",
		},
	}
}
