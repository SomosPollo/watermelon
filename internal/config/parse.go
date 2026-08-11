package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// ParseFile reads and parses a .watermelon.toml file
func ParseFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return Parse(data)
}

// Parse parses TOML config bytes
func Parse(data []byte) (*Config, error) {
	cfg := NewConfig()
	metadata, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return nil, fmt.Errorf("parsing config: unknown key(s): %s", strings.Join(keys, ", "))
	}
	return cfg, nil
}
