package artifact

import (
	"fmt"
	"os"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"gopkg.in/yaml.v3"
)

var manifestLog = logging.WithSource("artifact/manifest")

// ManifestFileName is the name of the metadata sidecar inside an artifact.
const ManifestFileName = "filemeta.yml"

// FileMeta records the permission and integrity metadata for a single
// absolute path inside an artifact. Git cannot track owner/group/mode, so
// this sidecar file is the source of truth for restoring them.
// See ADR-0008 (Artifact Storage Format).
type FileMeta struct {
	Path   string `yaml:"path"`
	Mode   string `yaml:"mode"`
	Owner  string `yaml:"owner"`
	Group  string `yaml:"group"`
	SHA256 string `yaml:"sha256,omitempty"`
	Type   string `yaml:"type,omitempty"` // "directory", or empty for a regular file

	// ModTime is the source file's modification time (RFC3339), recorded
	// so Restore can compare it against a same-path file already on disk
	// and keep whichever is newer, instead of always overwriting.
	ModTime string `yaml:"mod_time,omitempty"`
}

// Manifest is the root structure of filemeta.yml.
type Manifest struct {
	Version     string     `yaml:"version"`
	GeneratedAt string     `yaml:"generated_at"`
	Entries     []FileMeta `yaml:"entries"`
}

// LoadManifest reads and parses a filemeta.yml file.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &m, nil
}

// SaveManifest writes a Manifest to filemeta.yml.
func SaveManifest(path string, m *Manifest) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	manifestLog.Infof("Manifest written: %s (%d entries)", path, len(m.Entries))
	return nil
}
