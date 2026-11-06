package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

// startProcessAt runs the test binary, idle, as the executable at path.
func startProcessAt(t *testing.T, path string, args ...string) int {
	t.Helper()
	src, _ := os.Executable()
	os.MkdirAll(filepath.Dir(path), 0755)
	copyFile(t, src, path)
	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(), "CC_FAKE_DAEMON=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd.Process.Pid
}

func TestListClaudeCodeFindsClaudeCode(t *testing.T) {
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	pid := startProcessAt(t, filepath.Join(t.TempDir(), "npm", "claude-code", "bin", name))
	time.Sleep(200 * time.Millisecond) // let it show up in the process table

	if pids := listClaudeCode(); !slices.Contains(pids, pid) {
		t.Errorf("listClaudeCode() = %v, missing Claude Code at pid %d", pids, pid)
	}
}

// The Claude desktop app's executable is also claude.exe.
func TestListClaudeCodeSkipsDesktopApp(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the desktop app is named Claude on macOS, which pgrep -x claude doesn't match")
	}
	pid := startProcessAt(t, filepath.Join(t.TempDir(), "AnthropicClaude", "app-1.2.3", "claude.exe"))
	time.Sleep(200 * time.Millisecond)

	if pids := listClaudeCode(); slices.Contains(pids, pid) {
		t.Errorf("listClaudeCode() = %v includes the desktop app at pid %d", pids, pid)
	}
}

// Claude in Chrome keeps claude --chrome-native-host running for as long as
// Chrome is open; with it counted, the daemon never exited and an idle
// session was never cleared.
func TestListClaudeCodeSkipsHelpers(t *testing.T) {
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	chromeHost := startProcessAt(t, filepath.Join(t.TempDir(), name), "--chrome-native-host")
	mcpServer := startProcessAt(t, filepath.Join(t.TempDir(), name), "mcp", "serve")
	session := startProcessAt(t, filepath.Join(t.TempDir(), name), "--resume", "s1")
	time.Sleep(200 * time.Millisecond)

	pids := listClaudeCode()
	if slices.Contains(pids, chromeHost) || slices.Contains(pids, mcpServer) {
		t.Errorf("listClaudeCode() = %v includes a helper (Chrome native host %d, mcp serve %d)", pids, chromeHost, mcpServer)
	}
	if !slices.Contains(pids, session) {
		t.Errorf("listClaudeCode() = %v, missing the session at pid %d", pids, session)
	}
}

func TestClaudeCodeHelper(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{`C:\Users\x\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\bin\claude.exe`, "--chrome-native-host"}, true},
		{[]string{"/Users/x/.local/bin/claude", "--chrome-native-host"}, true},
		{[]string{"claude", "mcp", "serve"}, true},
		{[]string{"claude"}, false},
		{[]string{"claude", "--resume", "s1"}, false},
		{[]string{"claude", "mcp", "list"}, false},
		{[]string{"claude", "--output-format", "stream-json", "--no-chrome"}, false}, // IDE extension
		{[]string{"claude", "explain mcp serve"}, false},                             // a prompt, as on Windows
		{nil, false},
	}
	for _, tt := range tests {
		if got := claudeCodeHelper(tt.args); got != tt.want {
			t.Errorf("claudeCodeHelper(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
