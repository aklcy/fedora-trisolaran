package artifact

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// tmpfsMagic is the statfs f_type of a tmpfs mount (linux/magic.h). Pages
// written there live in RAM, so its free space is only as real as the
// memory actually available.
const tmpfsMagic = 0x01021994

// maxUse caps how much of a location's available space a pack may plan to
// fill. The rest stays free for everything else running on the machine --
// and absorbs tar headers, filemeta.yml and estimate error.
const maxUse = 0.70

// Estimate walks the paths exactly as Pack will (same excludes, symlinks
// not followed, missing paths skipped) and returns the total size of the
// regular files it would copy, plus how many there are.
func Estimate(paths, excludes []string) (bytes int64, files int, err error) {
	for _, src := range paths {
		src = filepath.Clean(src)
		if isExcluded(src, excludes) {
			continue
		}
		if _, statErr := os.Stat(src); os.IsNotExist(statErr) {
			continue
		}

		err = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if p != src && isExcluded(p, excludes) {
				if info != nil && info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				bytes += info.Size()
				files++
			}
			return nil
		})
		if err != nil {
			return bytes, files, fmt.Errorf("failed to estimate %s: %w", src, err)
		}
	}
	return bytes, files, nil
}

// Preflight refuses to start a pack that can't fit. Pack writes a full
// uncompressed mirror into stageDir and then the tgz to outputPath, so in
// the worst case (incompressible data) each needs the whole estimate; when
// both sit on the same filesystem it needs twice that. A tmpfs stageDir is
// additionally bounded by available RAM -- filling it starves the whole
// session, not just this process (this is how a dehydra once got
// gnome-shell OOM-killed).
func Preflight(estimate int64, stageDir, outputPath string) error {
	need := estimate

	stage, err := diskOf(stageDir)
	if err != nil {
		return fmt.Errorf("failed to inspect staging location %s: %w", stageDir, err)
	}
	out, err := diskOf(filepath.Dir(outputPath))
	if err != nil {
		return fmt.Errorf("failed to inspect output location %s: %w", filepath.Dir(outputPath), err)
	}

	if stage.dev == out.dev {
		if 2*need > stage.avail {
			return fmt.Errorf("staging and output share %s: need about %s (uncompressed copy + archive), only %s usable (70%% of free) -- pass --stage-dir or --output on another filesystem",
				stageDir, humanBytes(2*need), humanBytes(stage.avail))
		}
		return nil
	}

	if need > stage.avail {
		hint := ""
		if stage.tmpfs {
			hint = " (it's tmpfs, so this is RAM)"
		}
		return fmt.Errorf("staging location %s needs about %s, only %s usable (70%% of free)%s -- pass --stage-dir pointing at a real disk",
			stageDir, humanBytes(need), humanBytes(stage.avail), hint)
	}
	if need > out.avail {
		return fmt.Errorf("output location %s needs up to %s, only %s usable (70%% of free)",
			filepath.Dir(outputPath), humanBytes(need), humanBytes(out.avail))
	}
	return nil
}

type diskInfo struct {
	dev   uint64
	avail int64 // already scaled down to maxUse
	tmpfs bool
}

// diskOf reports the filesystem holding dir and how much of it a pack may
// use: free blocks (capped by MemAvailable when it's tmpfs), times maxUse.
func diskOf(dir string) (diskInfo, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return diskInfo{}, err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return diskInfo{}, err
	}
	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return diskInfo{}, fmt.Errorf("unable to read device of %s", dir)
	}

	info := diskInfo{
		dev:   sys.Dev,
		avail: int64(st.Bavail) * st.Bsize,
		tmpfs: st.Type == tmpfsMagic,
	}
	if info.tmpfs {
		mem, err := memAvailable()
		if err != nil {
			return diskInfo{}, err
		}
		info.avail = min(info.avail, mem)
	}
	info.avail = int64(float64(info.avail) * maxUse)
	return info, nil
}

// memAvailable reads MemAvailable from /proc/meminfo, in bytes.
func memAvailable() (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			packLog.Warnf("failed to close /proc/meminfo: %v", cerr)
		}
	}()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid MemAvailable %q: %w", fields[1], err)
			}
			return kb * 1024, nil
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("MemAvailable not found in /proc/meminfo")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
