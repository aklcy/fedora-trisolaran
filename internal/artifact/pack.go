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
	"strings"
	"syscall"
	"time"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var packLog = logging.WithSource("artifact/pack")

// Pack collects the given absolute paths into a single artifact tgz file.
// Each path is mirrored under fs/<absolute path> inside the archive, and
// its permission metadata (mode, owner, group, hash) is recorded in
// filemeta.yml. See ADR-0008 (Artifact Storage Format).
//
// If blueprintPath is non-empty, that file is copied verbatim into the
// archive as BlueprintFileName, so a future `tri rehydra <artifact>`
// can read the blueprint straight out of the artifact.
//
// Paths and excludes must already be expanded to absolute form (no "~").
// Anything matching an exclude (see isExcluded) is left out entirely, along
// with everything under it.
//
// The uncompressed mirror is staged under stageBase (empty means the
// system temp dir). Before anything is copied, the total size is estimated
// and checked against the free space of both the staging and output
// locations (see Preflight), so an oversized pack fails up front instead
// of filling a disk -- or RAM, when stageBase is tmpfs.
//
// Returns a human-readable notice for every path worth flagging -- missing
// entirely, missing partway up the tree, or present but empty -- so the
// caller can report them clearly. None of these are fatal (see
// collectPath); only a real read/copy failure aborts the pack.
func Pack(paths, excludes []string, blueprintPath, outputPath, stageBase string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths to pack")
	}

	if stageBase == "" {
		stageBase = os.TempDir()
	}

	estimate, files, err := Estimate(paths, excludes)
	if err != nil {
		return nil, err
	}
	packLog.Infof("Estimated %s across %d files, staging under %s", humanBytes(estimate), files, stageBase)
	if err := Preflight(estimate, stageBase, outputPath); err != nil {
		return nil, fmt.Errorf("not enough space, nothing was copied: %w", err)
	}

	packLog.Infof("Packing %d paths into %s", len(paths), outputPath)

	stageDir, err := os.MkdirTemp(stageBase, "trisolaran-pack-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(stageDir); rerr != nil {
			packLog.Warnf("failed to remove staging directory %s: %v", stageDir, rerr)
		}
	}()

	protectName := fmt.Sprintf("trisolaran-backup-%s", time.Now().Format("20060102"))
	root := filepath.Join(stageDir, protectName)
	fsRoot := filepath.Join(root, "fs")
	if err := os.MkdirAll(fsRoot, 0755); err != nil {
		return nil, fmt.Errorf("failed to create fs mirror root: %w", err)
	}

	manifest := &Manifest{
		Version:     "1.0",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	var notices []string
	for _, p := range paths {
		notice, err := collectPath(p, fsRoot, excludes, manifest)
		if err != nil {
			return notices, fmt.Errorf("failed to collect %s: %w", p, err)
		}
		if notice != "" {
			notices = append(notices, notice)
		}
	}

	if err := SaveManifest(filepath.Join(root, ManifestFileName), manifest); err != nil {
		return notices, err
	}

	if blueprintPath != "" {
		data, err := os.ReadFile(blueprintPath)
		if err != nil {
			return notices, fmt.Errorf("failed to read blueprint for embedding: %w", err)
		}
		if err := os.WriteFile(filepath.Join(root, BlueprintFileName), data, 0644); err != nil {
			return notices, fmt.Errorf("failed to embed blueprint: %w", err)
		}
	}

	if err := createTarGz(stageDir, protectName, outputPath); err != nil {
		return notices, fmt.Errorf("failed to create archive: %w", err)
	}

	packLog.Infof("Artifact created: %s (%d entries)", outputPath, len(manifest.Entries))
	return notices, nil
}

// describeMissing explains why src doesn't exist: either its immediate
// parent is present (src itself is simply missing), or the tree breaks
// somewhere higher up, in which case it reports the deepest missing
// ancestor -- e.g. "custom/" was never created, not just the file inside
// it.
func describeMissing(src string) string {
	dir := filepath.Dir(src)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Sprintf("target does not exist: %s", src)
	}

	broken := dir
	for {
		parent := filepath.Dir(broken)
		if parent == broken {
			break // reached filesystem root, still missing
		}
		if _, err := os.Stat(parent); err == nil {
			break
		}
		broken = parent
	}
	return fmt.Sprintf("path broken at %s (does not exist): %s", broken, src)
}

// describeIfEmpty reports a present-but-empty file or directory, so it
// doesn't silently produce an artifact entry with nothing in it.
func describeIfEmpty(src string, info os.FileInfo) string {
	if info.IsDir() {
		entries, err := os.ReadDir(src)
		if err == nil && len(entries) == 0 {
			return fmt.Sprintf("empty directory: %s", src)
		}
		return ""
	}
	if info.Mode().IsRegular() && info.Size() == 0 {
		return fmt.Sprintf("empty file (0 bytes): %s", src)
	}
	return ""
}

// collectPath walks a single source path (file or directory) and mirrors
// it under fsRoot, appending metadata entries to the manifest. Nothing
// here is fatal to the overall pack: a missing path (whether just the
// leaf or broken higher up the tree) is skipped, and a present-but-empty
// file/directory is still collected -- both are only reported back via
// the notice return value so the caller can warn about them.
func collectPath(src, fsRoot string, excludes []string, manifest *Manifest) (notice string, err error) {
	src = filepath.Clean(src)

	if isExcluded(src, excludes) {
		packLog.Infof("Excluded: %s", src)
		return "", nil
	}

	info, statErr := os.Stat(src)
	if os.IsNotExist(statErr) {
		msg := describeMissing(src)
		packLog.Warn(msg)
		return msg, nil
	}
	if statErr != nil {
		return "", statErr
	}

	if msg := describeIfEmpty(src, info); msg != "" {
		packLog.Warn(msg)
		notice = msg
	}

	err = filepath.Walk(src, func(walkedPath string, info os.FileInfo, err error) error {
		// Checked before err: filepath.Walk lists a directory before calling
		// us for it, so an excluded directory we can't read (e.g. owned by
		// another user) arrives here with a permission error we must ignore.
		if walkedPath != src && isExcluded(walkedPath, excludes) {
			packLog.Infof("Excluded: %s", walkedPath)
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if err != nil {
			return err
		}

		mirrorPath := filepath.Join(fsRoot, walkedPath)
		owner, group, err := lookupOwnerGroup(info)
		if err != nil {
			return err
		}
		mode := fmt.Sprintf("%04o", info.Mode().Perm())

		// Walk reports symlinks via Lstat and never follows them, so the
		// link itself is recorded -- dangling targets included.
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(walkedPath)
			if err != nil {
				return fmt.Errorf("failed to read symlink %s: %w", walkedPath, err)
			}
			manifest.Entries = append(manifest.Entries, FileMeta{
				Path:    walkedPath,
				Mode:    mode,
				Owner:   owner,
				Group:   group,
				Type:    "symlink",
				Target:  target,
				ModTime: info.ModTime().UTC().Format(time.RFC3339),
			})
			return nil
		}

		if info.IsDir() {
			if err := os.MkdirAll(mirrorPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("failed to create mirror directory: %w", err)
			}
			manifest.Entries = append(manifest.Entries, FileMeta{
				Path:  walkedPath,
				Mode:  mode,
				Owner: owner,
				Group: group,
				Type:  "directory",
			})
			return nil
		}

		// Sockets, FIFOs and devices are skipped; only regular files are mirrored.
		if !info.Mode().IsRegular() {
			packLog.Warnf("Skipping non-regular file: %s", walkedPath)
			return nil
		}

		if err := copyFile(walkedPath, mirrorPath, info.Mode().Perm()); err != nil {
			return fmt.Errorf("failed to copy %s: %w", walkedPath, err)
		}

		sum, err := sha256File(walkedPath)
		if err != nil {
			return err
		}

		manifest.Entries = append(manifest.Entries, FileMeta{
			Path:    walkedPath,
			Mode:    mode,
			Owner:   owner,
			Group:   group,
			SHA256:  sum,
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})

		return nil
	})
	return notice, err
}

// isExcluded reports whether path is covered by any exclude: an exact
// match, anything under an excluded directory, or -- for an exclude
// containing glob characters -- a filepath.Match on the whole path.
func isExcluded(path string, excludes []string) bool {
	for _, ex := range excludes {
		if path == ex || strings.HasPrefix(path, ex+string(filepath.Separator)) {
			return true
		}
		if strings.ContainsAny(ex, "*?[") {
			if ok, _ := filepath.Match(ex, path); ok {
				return true
			}
		}
	}
	return false
}

// lookupOwnerGroup resolves the owner/group *names* for a file, per
// ADR-0008: UID/GID are never persisted, only names.
func lookupOwnerGroup(info os.FileInfo) (owner, group string, err error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", fmt.Errorf("unable to read owner/group metadata")
	}

	u, err := user.LookupId(strconv.FormatUint(uint64(stat.Uid), 10))
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve owner name: %w", err)
	}

	g, err := user.LookupGroupId(strconv.FormatUint(uint64(stat.Gid), 10))
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve group name: %w", err)
	}

	return u.Username, g.Name, nil
}

// copyFile copies src to dest. The destination's Close() error is checked
// and returned: a failed Close can mean buffered data was never flushed to
// disk, which would silently corrupt the copy.
func copyFile(src, dest string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := in.Close(); cerr != nil {
			packLog.Warnf("failed to close source file %s: %v", src, cerr)
		}
	}()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // best-effort; the copy error is the one that matters here
		return err
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to close %s: %w", dest, err)
	}

	return nil
}

// createTarGz packs baseDir/name into a gzip-compressed tarball at outputPath.
//
// Closing tw/gw/outFile (in that order) is what actually flushes the tar
// footer and compressed stream to disk, so a failed Close here means a
// truncated/corrupt archive. Those errors are surfaced via the named
// return instead of being silently deferred away.
func createTarGz(baseDir, name, outputPath string) (err error) {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := outFile.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close archive file: %w", cerr)
		}
	}()

	gw := gzip.NewWriter(outFile)
	defer func() {
		if cerr := gw.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close gzip writer: %w", cerr)
		}
	}()

	tw := tar.NewWriter(gw)
	defer func() {
		if cerr := tw.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close tar writer: %w", cerr)
		}
	}()

	root := filepath.Join(baseDir, name)
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			if cerr := f.Close(); cerr != nil {
				packLog.Warnf("failed to close source file %s: %v", path, cerr)
			}
		}()

		_, err = io.Copy(tw, f)
		return err
	})

	return err
}
