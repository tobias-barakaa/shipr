// Package procgroup isolates Unix process-group handling (Linux/macOS).
package procgroup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Configure makes cmd the leader of a new process group (pgid == child pid).
func Configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// Signal sends sig to every process in the group. A group that no longer
// exists is not an error. It refuses pgid <= 1 because kill(-1) hits everything.
func Signal(pgid int, sig syscall.Signal) error {
	if pgid <= 1 {
		return fmt.Errorf("refusing to signal process group %d", pgid)
	}
	err := syscall.Kill(-pgid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// Alive reports whether the group still has a live (non-zombie) member.
func Alive(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	if err := syscall.Kill(-pgid, 0); err != nil {
		return errors.Is(err, syscall.EPERM)
	}
	// Signal 0 also succeeds for zombies, so refine using /proc when present.
	if n, ok := liveMembers(pgid); ok {
		return n > 0
	}
	return true
}

func liveMembers(pgid int) (int, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	n := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(data)
		i := strings.LastIndexByte(s, ')') // the command name may contain spaces
		if i < 0 || i+2 >= len(s) {
			continue
		}
		f := strings.Fields(s[i+2:]) // state ppid pgrp ...
		if len(f) < 3 || f[0] == "Z" {
			continue
		}
		if g, err := strconv.Atoi(f[2]); err == nil && g == pgid {
			n++
		}
	}
	return n, true
}
