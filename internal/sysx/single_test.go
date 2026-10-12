package sysx

import (
	"fmt"
	"testing"
	"time"
)

func TestSingleInstance(t *testing.T) {
	name := fmt.Sprintf("ytcs-test-%d", time.Now().UnixNano())
	a, ok := AcquireSingle(name, 0)
	if !ok {
		t.Fatal("first copy must get the lock")
	}
	if _, ok := AcquireSingle(name, 0); ok {
		t.Fatal("a second copy must not get the lock")
	}
	go func() { time.Sleep(300 * time.Millisecond); a.Release() }()
	b, ok := AcquireSingle(name, 3*time.Second)
	if !ok {
		t.Fatal("waiting must get the lock once it is released")
	}
	b.Release()
}
