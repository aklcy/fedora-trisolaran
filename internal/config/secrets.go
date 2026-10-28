package config

import (
	"fmt"
	"os"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"gopkg.in/yaml.v3"
)

var log = logging.WithSource("secrets")

// Secrets defines the schema for the secrets configuration file.
// We use YAML tags here to map keys from the input file.
type Secrets struct {
	LuksPassword string `yaml:"luks_password"`
	// Add other secrets here as needed, e.g.:
	// GitToken     string `yaml:"git_token"`
	// RootPassword string `yaml:"root_password"`
}

// LoadSecrets reads and parses the secrets YAML file from the given path.
func LoadSecrets(path string) (*Secrets, error) {
	log.Infof("Loading secrets from local file: %s", path)

	// 1. Sanity check: file existence
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("secret file not found at: %s", path)
	}

	// 2. Read file content
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// 3. Parse YAML
	var book Secrets
	if err := yaml.Unmarshal(data, &book); err != nil {
		return nil, fmt.Errorf("failed to parse YAML structure: %w", err)
	}

	return &book, nil
}
