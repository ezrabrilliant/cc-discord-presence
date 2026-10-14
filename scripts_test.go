package main

import (
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
	h := &scriptHome{t: t, bash: findBash(t), dir: t.TempDir()}
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

// run executes scripts/<name> the way a hook does, feeding stdin as the hook
// input JSON.
func (h *scriptHome) run(name, stdin string) string {
	h.t.Helper()
	cmd := exec.Command(h.bash, filepath.Join("scripts", name))
	cmd.Env = append(os.Environ(), "HOME="+h.dir, "CC_FAKE_DAEMON=1")
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
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
