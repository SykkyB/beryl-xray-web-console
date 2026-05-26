//go:build !linux

package vetlib

import "os/exec"

// setKillOnParentExit is a no-op on platforms without SysProcAttr.Pdeathsig.
// Only macOS/dev builds hit this path — the production binary is
// always linux/arm64 cross-compiled and uses proc_linux.go.
func setKillOnParentExit(cmd *exec.Cmd) {}
