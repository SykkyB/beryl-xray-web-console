//go:build linux

package vetlib

import (
	"os/exec"
	"syscall"
)

// setKillOnParentExit asks the kernel to deliver SIGKILL to the child
// when the panel process (us) dies — Pdeathsig in SysProcAttr.
// Important because procd sends SIGKILL after a 5s SIGTERM grace if
// the panel didn't shut down cleanly (e.g. during a deploy restart
// while a scan is mid-flight). Without this, deep-probe sing-box
// children get reparented to init and keep running, eating CPU/RAM
// until each finishes its own 10s probe timeout — visible to the
// admin as orphaned `vless-vet-deep-*.json`-loaded sing-box procs.
//
// Linux-only; the field doesn't exist on Darwin's SysProcAttr.
func setKillOnParentExit(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
