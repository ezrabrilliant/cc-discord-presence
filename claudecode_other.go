//go:build !windows

package main

import (
	"os/exec"
	"strconv"
	"strings"
)

// listClaudeCode returns the pids of running Claude Code sessions: processes
// named claude, except helpers (claudeCodeHelper). The macOS desktop app is
// named Claude, which pgrep -x doesn't match.
func listClaudeCode() []int {
	out, _ := exec.Command("pgrep", "-x", "claude").Output()
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		// pgrep -x matches the name only. ps joins the arguments with
		// spaces, which is enough to recognize a helper.
		args, err := exec.Command("ps", "-p", field, "-o", "args=").Output()
		if err != nil {
			continue // exited meanwhile
		}
		if !claudeCodeHelper(strings.Fields(string(args))) {
			pids = append(pids, pid)
		}
	}
	return pids
}
