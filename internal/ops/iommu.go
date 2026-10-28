package ops

import (
	"os"
	"strings"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var iommuLog = logging.WithSource("ops/iommu")

// CheckIOMMUNotDisabled warns (never fails) if the currently running
// kernel was booted with amd_iommu=off. Some GPU-tuning guides recommend
// that flag, but it also stops the kernel from enumerating NPU devices --
// a common trap for AMD XDNA/NPU setups. Returns a non-empty notice when
// the conflicting parameter is present, so the caller can surface it in
// the final run report; the fix (edit the bootloader config and reboot)
// is left to the user, since it's a boot-time change this tool shouldn't
// make unattended.
func CheckIOMMUNotDisabled() string {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		iommuLog.Warnf("Failed to read /proc/cmdline: %v", err)
		return ""
	}

	for _, field := range strings.Fields(string(data)) {
		if field == "amd_iommu=off" {
			msg := "kernel boot parameters contain amd_iommu=off, which prevents the kernel from enumerating NPU devices -- remove it from your bootloader config and reboot"
			iommuLog.Warn(msg)
			return msg
		}
	}

	return ""
}
