package sysx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLaunchHelper is re-executed by TestLaunch: it writes its arguments
// and working folder to $SYSX_LAUNCH and exits.
func TestLaunchHelper(t *testing.T) {
	out := os.Getenv("SYSX_LAUNCH")
	if out == "" {
		return
	}
	wd, _ := os.Getwd()
	_ = os.WriteFile(out+".tmp", []byte(strings.Join(os.Args[len(os.Args)-1:], ",")+"|"+wd), 0o644)
	_ = os.Rename(out+".tmp", out)
	os.Exit(0)
}

func TestLaunch(t *testing.T) {
	out := filepath.Join(t.TempDir(), "launched.txt")
	t.Setenv("SYSX_LAUNCH", out)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := Launch(exe, "-test.run=^TestLaunchHelper$", "--", "--updated-from=1.0.0"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		b, err := os.ReadFile(out)
		if err == nil {
			args, wd, _ := strings.Cut(string(b), "|")
			if args != "--updated-from=1.0.0" {
				t.Fatalf("args = %q", args)
			}
			if r1, _ := filepath.EvalSymlinks(wd); r1 != "" {
				r2, _ := filepath.EvalSymlinks(filepath.Dir(exe))
				if !strings.EqualFold(r1, r2) {
					t.Fatalf("working folder %q, want the program's folder %q", r1, r2)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the launched program did not run")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestLaunchEmpty(t *testing.T) {
	if err := Launch(" "); err == nil {
		t.Fatal("empty name accepted")
	}
}
