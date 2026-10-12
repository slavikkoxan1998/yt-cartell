//go:build windows

package update

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRunningHelper idles while TestSwapRunningExe replaces its exe.
func TestRunningHelper(t *testing.T) {
	if os.Getenv("UPDATE_RUNNING_HELPER") != "1" {
		return
	}
	os.Stdout.WriteString("ready\n")
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

// The Windows install renames the RUNNING exe: check it on a real process
// (a copy of this test binary), and that the .old goes once it has exited.
func TestSwapRunningExe(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	app := filepath.Join(dir, "app.exe")
	copyTo := func(dst string) {
		in, err := os.Open(self)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		out, err := os.Create(dst)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatal(err)
		}
		out.Close()
	}
	copyTo(app)
	cmd := exec.Command(app, "-test.run=^TestRunningHelper$")
	cmd.Env = append(os.Environ(), "UPDATE_RUNNING_HELPER=1")
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "ready\n" {
		t.Fatalf("helper said %q", line)
	}

	staged := filepath.Join(t.TempDir(), "new.exe")
	if err := os.WriteFile(staged, []byte("new build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapFile(staged, app); err != nil {
		t.Fatalf("swap while running: %v", err)
	}
	if b, _ := os.ReadFile(app); string(b) != "new build" {
		t.Fatal("the new build is not in place")
	}
	// The running image cannot be deleted yet …
	if err := os.Remove(app + OldSuffix); err == nil {
		t.Fatal("removed the exe of a running process?")
	}
	// … and can once the old process has exited.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for {
		left := leftovers(app)
		for _, l := range left {
			_ = os.Remove(l)
		}
		if len(leftovers(app)) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leftovers stay: %v", leftovers(app))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
