package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var hashLog = logging.WithSource("artifact/hash")

// sha256File computes the SHA256 hash of a file's contents.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file for hashing: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			hashLog.Warnf("failed to close %s: %v", path, cerr)
		}
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
