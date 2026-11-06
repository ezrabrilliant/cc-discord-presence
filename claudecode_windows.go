//go:build windows

package main

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// listClaudeCode returns the pids of running Claude Code sessions: every
// claude.exe except the Claude desktop app and helpers.
func listClaudeCode() []int {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)

	var pids []int
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "claude.exe") && claudeCodeSession(entry.ProcessID) {
			pids = append(pids, int(entry.ProcessID))
		}
	}
	return pids
}

// claudeCodeSession reports whether the claude.exe at pid runs a Claude Code
// session. The Claude desktop app's executable has the same name and lives
// under AnthropicClaude; helpers are told apart by their command line
// (claudeCodeHelper). A process that can't be inspected counts.
func claudeCodeSession(pid uint32) bool {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return true
	}
	defer windows.CloseHandle(process)
	if strings.Contains(strings.ToLower(processPath(process)), `\anthropicclaude\`) {
		return false
	}
	return !claudeCodeHelper(processArgs(process))
}

func processPath(process windows.Handle) string {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(process, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// processArgs returns another process's command line, split into arguments.
func processArgs(process windows.Handle) []string {
	// The first call fails with the size the command line needs
	var size uint32
	windows.NtQueryInformationProcess(process, windows.ProcessCommandLineInformation, nil, 0, &size)
	if size == 0 {
		return nil
	}
	buf := make([]byte, size)
	if err := windows.NtQueryInformationProcess(process, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), size, &size); err != nil {
		return nil
	}
	args, _ := windows.DecomposeCommandLine((*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String())
	return args
}
