package artifact

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var unpackLog = logging.WithSource("artifact/unpack")

// Restore extracts an artifact tgz and applies its contents back onto the
// system: files are copied to their recorded absolute paths, then
// owner/group/mode are applied from filemeta.yml and file content is
// verified against the recorded SHA256. See ADR-0008 (Artifact Storage Format).
//
// trustTimestamps controls how a path that already exists on disk is
// handled: when true, the newer of the on-disk file and the artifact's
// recorded ModTime wins; when false (e.g. the system clock isn't
// NTP-synced, so timestamps aren't trustworthy), the on-disk file always
// wins. Either way, keeping the on-disk file is never fatal -- it's
// reported back via the returned notices instead.
func Restore(archivePath string, trustTimestamps bool) ([]string, error) {
	unpackLog.Infof("Restoring artifact: %s", archivePath)

	stageDir, err := os.MkdirTemp("", "trisolaran-unpack-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(stageDir); rerr != nil {
			unpackLog.Warnf("failed to remove staging directory %s: %v", stageDir, rerr)
		}
	}()

	if err := extractTarGz(archivePath, stageDir); err != nil {
		return nil, fmt.Errorf("failed to extract archive: %w", err)
	}

	root, err := findArtifactRoot(stageDir)
	if err != nil {
		return nil, err
	}

	manifest, err := LoadManifest(filepath.Join(root, ManifestFileName))
	if err != nil {
		return nil, err
	}

	fsRoot := filepath.Join(root, "fs")

	var notices []string
	for _, entry := range manifest.Entries {
		notice, err := applyEntry(entry, fsRoot, trustTimestamps)
		if err != nil {
			return notices, fmt.Errorf("failed to restore %s: %w", entry.Path, err)
		}
		if notice != "" {
			notices = append(notices, notice)
		}
	}

	unpackLog.Infof("Artifact restored: %d entries applied", len(manifest.Entries))
	return notices, nil
}

// shouldKeepLocal decides whether an already-existing on-disk file should
// be kept as-is instead of being overwritten by the artifact's copy.
func shouldKeepLocal(entry FileMeta, trustTimestamps bool) (keep bool, notice string) {
	localInfo, err := os.Stat(entry.Path)
	if err != nil {
		return false, "" // nothing on disk to conflict with; restore normally
	}

	if !trustTimestamps {
		return true, fmt.Sprintf("kept on-disk file (time sync unavailable, can't compare timestamps): %s", entry.Path)
	}

	artifactTime, err := time.Parse(time.RFC3339, entry.ModTime)
	if err != nil {
		return true, fmt.Sprintf("kept on-disk file (artifact has no recorded mod time): %s", entry.Path)
	}

	if localInfo.ModTime().After(artifactTime) {
		return true, fmt.Sprintf("kept newer on-disk file over artifact's older copy: %s", entry.Path)
	}

	return false, ""
}

// findArtifactRoot locates the single "trisolaran-backup-<date>" directory
// produced by Pack inside the extracted archive.
func findArtifactRoot(stageDir string) (string, error) {
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return "", fmt.Errorf("failed to read staging directory: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(stageDir, e.Name()), nil
		}
	}

	return "", fmt.Errorf("archive does not contain a backup directory")
}

// applyEntry restores a single manifest entry: content, mode, and
// ownership. For a regular file that already exists on disk, it may keep
// the on-disk version instead (see shouldKeepLocal) -- in which case
// nothing about that entry is touched, and why is reported via notice.
func applyEntry(entry FileMeta, fsRoot string, trustTimestamps bool) (notice string, err error) {
	if entry.Type == "symlink" {
		return applySymlink(entry)
	}

	mode, err := parseMode(entry.Mode)
	if err != nil {
		return "", err
	}

	if entry.Type == "directory" {
		if err := os.MkdirAll(entry.Path, mode); err != nil {
			return "", fmt.Errorf("failed to create directory: %w", err)
		}
	} else {
		if keep, msg := shouldKeepLocal(entry, trustTimestamps); keep {
			return msg, nil
		}

		src := filepath.Join(fsRoot, entry.Path)

		if err := copyFile(src, entry.Path, mode); err != nil {
			return "", fmt.Errorf("failed to restore file content: %w", err)
		}

		if entry.SHA256 != "" {
			sum, err := sha256File(entry.Path)
			if err != nil {
				return "", err
			}
			if sum != entry.SHA256 {
				return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", entry.Path, entry.SHA256, sum)
			}
		}
	}

	if err := os.Chmod(entry.Path, mode); err != nil {
		return "", fmt.Errorf("failed to chmod: %w", err)
	}

	uid, gid, err := lookupUIDGID(entry.Owner, entry.Group)
	if err != nil {
		return "", err
	}
	if err := os.Chown(entry.Path, uid, gid); err != nil {
		return "", fmt.Errorf("failed to chown: %w", err)
	}

	return "", nil
}

// applySymlink recreates a symlink entry. A link already pointing at the
// same target only gets its ownership refreshed; anything else already at
// that path is kept as-is and reported, same as a kept regular file --
// replacing it could silently drop real content. Mode is not applied:
// Linux ignores permission bits on symlinks.
func applySymlink(entry FileMeta) (notice string, err error) {
	if existing, err := os.Readlink(entry.Path); err == nil {
		if existing != entry.Target {
			return fmt.Sprintf("kept on-disk symlink (points to %s, artifact has %s): %s", existing, entry.Target, entry.Path), nil
		}
	} else if _, err := os.Lstat(entry.Path); err == nil {
		return fmt.Sprintf("kept on-disk path instead of restoring symlink to %s: %s", entry.Target, entry.Path), nil
	} else {
		if err := os.MkdirAll(filepath.Dir(entry.Path), 0755); err != nil {
			return "", fmt.Errorf("failed to create parent directory: %w", err)
		}
		if err := os.Symlink(entry.Target, entry.Path); err != nil {
			return "", fmt.Errorf("failed to create symlink: %w", err)
		}
	}

	uid, gid, err := lookupUIDGID(entry.Owner, entry.Group)
	if err != nil {
		return "", err
	}
	if err := os.Lchown(entry.Path, uid, gid); err != nil {
		return "", fmt.Errorf("failed to lchown: %w", err)
	}

	return "", nil
}

func parseMode(mode string) (os.FileMode, error) {
	v, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q: %w", mode, err)
	}
	return os.FileMode(v), nil
}

// lookupUIDGID resolves owner/group *names* back to numeric IDs on the
// current system. Per ADR-0008, IDs are never trusted from the artifact
// itself — only names, resolved fresh at restore time.
func lookupUIDGID(owner, group string) (int, int, error) {
	u, err := user.Lookup(owner)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to resolve owner %q: %w", owner, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid uid for %q: %w", owner, err)
	}

	g, err := user.LookupGroup(group)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to resolve group %q: %w", group, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid gid for %q: %w", group, err)
	}

	return uid, gid, nil
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			unpackLog.Warnf("failed to close archive %s: %v", archivePath, cerr)
		}
	}()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := gr.Close(); cerr != nil {
			unpackLog.Warnf("failed to close gzip reader: %v", cerr)
		}
	}()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close() // best-effort; the copy error is the one that matters here
				return err
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("failed to close %s: %w", target, err)
			}
		}
	}

	return nil
}
