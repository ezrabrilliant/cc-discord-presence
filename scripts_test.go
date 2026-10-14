package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The hook scripts are tested by running them against a throwaway HOME. The
// test binary doubles as the daemon: started with CC_FAKE_DAEMON=1 it only
// stays alive, so no test talks to Discord.
func TestMain(m *testing.M) {
	if os.Getenv("CC_FAKE_DAEMON") == "1" {
		setupLog(os.Args[1:])
		fmt.Println("fake daemon running")
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// findBash returns the bash the plugin hooks run under: Git Bash on Windows
// (System32\bash.exe would be WSL), the system bash elsewhere.
func findBash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		if p, err := exec.LookPath("bash"); err == nil {
			return p
		}
		t.Skip("bash not found")
	}
	if p := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); p != "" {
		return p
	}
	if git, err := exec.LookPath("git"); err == nil {
		for _, rel := range []string{`..\bin\bash.exe`, `..\..\bin\bash.exe`} {
			p := filepath.Join(filepath.Dir(git), rel)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	t.Skip("Git Bash not found")
	return ""
}

type scriptHome struct {
	t    *testing.T
	bash string
	dir  string // stands in for $HOME
}

func newScriptHome(t *testing.T) *scriptHome {
	return newScriptHomeAt(t, t.TempDir())
}

func newScriptHomeAt(t *testing.T, dir string) *scriptHome {
	h := &scriptHome{t: t, bash: findBash(t), dir: dir}
	os.MkdirAll(h.claude("bin"), 0755)
	t.Cleanup(func() {
		if pid := h.pidFile(); pid != 0 && h.daemonRunning(pid) {
			killPid(pid)
		}
	})
	return h
}

func (h *scriptHome) claude(elem ...string) string {
	return filepath.Join(append([]string{h.dir, ".claude"}, elem...)...)
}

// installFakeDaemon puts the test binary where start.sh expects the daemon,
// with a version file that matches the script so nothing is downloaded.
func (h *scriptHome) installFakeDaemon() {
	h.t.Helper()
	src, err := os.Executable()
	if err != nil {
		h.t.Fatal(err)
	}
	bin := h.claude("bin", daemonBinaryName())
	copyFile(h.t, src, bin)
	os.WriteFile(bin+".version", []byte(scriptVersion(h.t)+"\n"), 0644)
}

// run executes scripts/<name> the way a hook does: input JSON on stdin,
// output read from a pipe until it closes.
func (h *scriptHome) run(name, stdin string) string {
	h.t.Helper()
	cmd := exec.Command(h.bash, filepath.Join("scripts", name))
	cmd.Env = append(os.Environ(), "HOME="+h.dir, "CC_FAKE_DAEMON=1")
	cmd.Stdin = strings.NewReader(stdin)
	// A daemon that inherits the script's output pipe keeps it open after
	// the script exits; Claude Code then waits on the hook until it times out.
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.CombinedOutput()
	if errors.Is(err, exec.ErrWaitDelay) {
		h.t.Fatalf("%s exited but its output pipe stayed open (inherited by the daemon)\n%s", name, out)
	}
	if err != nil {
		h.t.Fatalf("%s failed: %v\n%s", name, err, out)
	}
	return string(out)
}

func (h *scriptHome) pidFile() int {
	data, _ := os.ReadFile(h.claude("discord-presence.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

func (h *scriptHome) writePidFile(pid int) {
	os.WriteFile(h.claude("discord-presence.pid"), []byte(strconv.Itoa(pid)), 0644)
}

// daemonRunning reports whether pid is a live process running the daemon
// binary (here: the fake one start.sh launched).
func (h *scriptHome) daemonRunning(pid int) bool {
	if runtime.GOOS == "windows" {
		out, _ := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
		return strings.Contains(string(out), `"`+daemonBinaryName()+`"`)
	}
	out, _ := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	return strings.Contains(string(out), daemonBinaryName())
}

// startUnrelatedProcess starts a long-lived process that is not the daemon,
// standing in for whatever reuses a stale pid. It reports whether it is
// still alive.
func startUnrelatedProcess(t *testing.T) (pid int, alive func() bool) {
	t.Helper()
	src, _ := os.Executable()
	name := "unrelated"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(t.TempDir(), name)
	copyFile(t, src, exe)

	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "CC_FAKE_DAEMON=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	t.Cleanup(func() { cmd.Process.Kill(); <-exited })

	return cmd.Process.Pid, func() bool {
		select {
		case <-exited:
			return false
		case <-time.After(200 * time.Millisecond):
			return true
		}
	}
}

func daemonBinaryName() string {
	name := fmt.Sprintf("cc-discord-presence-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func scriptVersion(t *testing.T) string {
	data, err := os.ReadFile(filepath.Join("scripts", "start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^VERSION="(v[^"]+)"`).FindSubmatch(data)
	if m == nil {
		t.Fatal("VERSION not found in start.sh")
	}
	return string(m[1])
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
}

func killPid(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
}

// hookInput is the JSON Claude Code sends a SessionStart/SessionEnd hook.
func hookInput(t *testing.T, sessionID, transcript string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{"session_id": sessionID, "transcript_path": transcript})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// waitDaemonStopped polls, since taskkill returns before the process is gone.
func (h *scriptHome) waitDaemonStopped(pid int) bool {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if !h.daemonRunning(pid) {
			return true
		}
	}
	return false
}

func skipUnlessWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("on macOS and Linux sessions are tracked by the parent pid")
	}
}

// Compaction fires SessionStart again for the same session, with no
// SessionEnd to match it.
func TestScriptsStopDaemonAfterCompactedSession(t *testing.T) {
	skipUnlessWindows(t)
	h := newScriptHome(t)
	h.installFakeDaemon()

	h.run("start.sh", hookInput(t, "session-a", ""))
	pid := h.pidFile()
	h.run("start.sh", hookInput(t, "session-a", "")) // after /compact
	h.run("stop.sh", hookInput(t, "session-a", ""))

	if !h.waitDaemonStopped(pid) {
		t.Error("daemon still running after the only session ended")
	}
}

func TestScriptsKeepDaemonWhileAnotherSessionRuns(t *testing.T) {
	skipUnlessWindows(t)
	h := newScriptHome(t)
	h.installFakeDaemon()

	h.run("start.sh", hookInput(t, "session-a", ""))
	pid := h.pidFile()
	h.run("start.sh", hookInput(t, "session-b", ""))
	h.run("stop.sh", hookInput(t, "session-a", ""))

	if !h.daemonRunning(pid) {
		t.Fatal("daemon stopped while session-b still runs")
	}

	h.run("stop.sh", hookInput(t, "session-b", ""))
	if !h.waitDaemonStopped(pid) {
		t.Error("daemon still running after both sessions ended")
	}
}

// A session that crashed or whose window was closed never runs SessionEnd;
// its transcript stops changing.
func TestScriptsIgnoreSessionThatEndedWithoutHook(t *testing.T) {
	skipUnlessWindows(t)
	h := newScriptHome(t)
	h.installFakeDaemon()

	crashed := filepath.Join(t.TempDir(), "crashed.jsonl")
	os.WriteFile(crashed, []byte("{}\n"), 0644)
	h.run("start.sh", hookInput(t, "session-crashed", crashed)) // no stop.sh follows
	old := time.Now().Add(-13 * time.Hour)
	os.Chtimes(crashed, old, old)

	h.run("start.sh", hookInput(t, "session-a", ""))
	pid := h.pidFile()
	h.run("stop.sh", hookInput(t, "session-a", ""))

	if !h.waitDaemonStopped(pid) {
		t.Error("daemon kept running for a session idle for 13 hours")
	}
}

// A pid file left behind by a daemon that is gone (reboot, Discord not
// running) can name a pid the OS has since given to another program.
func TestStopScriptSparesProcessThatReusedDaemonPid(t *testing.T) {
	h := newScriptHome(t)
	pid, alive := startUnrelatedProcess(t)
	h.writePidFile(pid)

	h.run("stop.sh", "")

	if !alive() {
		t.Errorf("stop.sh killed pid %d, which is not the daemon", pid)
	}
}

// The README tells users to check ~/.claude/discord-presence.log; it is the
// only place a Discord connection error shows up. The home has a space, as
// Windows profile folders like "C:\Users\John Doe" do.
func TestStartScriptWritesDaemonLog(t *testing.T) {
	h := newScriptHomeAt(t, filepath.Join(t.TempDir(), "John Doe"))
	h.installFakeDaemon()

	h.run("start.sh", "")

	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(h.claude("discord-presence.log"))
		if strings.Contains(string(data), "fake daemon running") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon output never reached discord-presence.log (got %q)", data)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestStartScriptStartsDaemonWhenPidFileNamesAnotherProcess(t *testing.T) {
	h := newScriptHome(t)
	h.installFakeDaemon()
	pid, _ := startUnrelatedProcess(t)
	h.writePidFile(pid)

	out := h.run("start.sh", "")

	if got := h.pidFile(); got == pid || !h.daemonRunning(got) {
		t.Errorf("start.sh did not start the daemon (pid file: %d, unrelated process: %d)\n%s", got, pid, out)
	}
}
