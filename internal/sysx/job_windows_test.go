//go:build windows

package sysx

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestJobHelper is re-executed as a child: "sleep" prints ready and idles,
// "parent" starts a sleeper through Start, reports its pid and exits at once
// (what the app does on window close: os.Exit with ffmpeg still running).
func TestJobHelper(t *testing.T) {
	switch os.Getenv("SYSX_JOB") {
	case "sleep":
		fmt.Println("ready")
		time.Sleep(60 * time.Second)
		os.Exit(0)
	case "parent":
		c := sleeper(context.Background())
		out, err := c.StdoutPipe()
		if err == nil {
			err = Start(c)
		}
		if err != nil {
			fmt.Println("error:", err)
			os.Exit(2)
		}
		line, _ := bufio.NewReader(out).ReadString('\n')
		fmt.Printf("pid:%d %s\n", c.Process.Pid, strings.TrimSpace(line))
		os.Exit(0)
	}
}

func sleeper(ctx context.Context) *exec.Cmd {
	c := Command(ctx, os.Args[0], "-test.run=^TestJobHelper$")
	c.Env = append(os.Environ(), "SYSX_JOB=sleep")
	return c
}

func TestStartJoinsAppJobAndResumes(t *testing.T) {
	if appJob() == 0 {
		t.Fatal("no kill-on-close job")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := sleeper(ctx)
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := Start(c); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	if c.SysProcAttr.CreationFlags&createSuspended != 0 {
		t.Fatal("caller's flags were changed")
	}
	in := false
	_ = c.Process.WithHandle(func(h uintptr) { in = inAppJob(h) })
	if !in {
		t.Fatal("child is not in the app job")
	}
	// It runs (was resumed): it prints ready.
	line, err := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("child did not run: %q %v", line, err)
	}
}

func TestRunAndOutput(t *testing.T) {
	c := Command(context.Background(), os.Args[0], "-test.run=^TestHelperProcess$", "--", "x", "y")
	c.Env = append(os.Environ(), "SYSX_HELPER=1")
	out, err := Output(c)
	if err != nil || !strings.Contains(string(out), "helper:x,y") {
		t.Fatalf("output %q %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := Command(ctx, os.Args[0], "-test.run=^TestHelperProcess$")
	d.Env = append(os.Environ(), "SYSX_HELPER=1")
	if err := Run(d); err == nil {
		t.Fatal("cancelled context must fail")
	}
}

// The regression: a child started through Start must not outlive the app.
func TestChildDiesWithApp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := Command(ctx, os.Args[0], "-test.run=^TestJobHelper$")
	c.Env = append(os.Environ(), "SYSX_JOB=parent")
	c.WaitDelay = 5 * time.Second
	out, err := c.Output()
	if err != nil {
		t.Fatalf("parent: %v %q", err, out)
	}
	s := strings.TrimSpace(string(out))
	var pid int
	if _, err := fmt.Sscanf(s, "pid:%d ready", &pid); err != nil || pid <= 0 {
		t.Fatalf("parent output %q", s)
	}
	const synchronize = 0x00100000
	h, err := syscall.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer syscall.CloseHandle(h)
	ev, _ := syscall.WaitForSingleObject(h, 10_000)
	if ev != syscall.WAIT_OBJECT_0 {
		t.Fatalf("child %s outlived its parent", strconv.Itoa(pid))
	}
}
