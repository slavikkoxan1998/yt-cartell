//go:build windows

package sysx

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// Windows does not kill child processes when their parent exits. Children
// started through Start/Run/Output join one process-wide Job Object with
// KILL_ON_JOB_CLOSE: when the app ends — normally, via os.Exit or a crash —
// Windows closes the last job handle and kills them, so an invisible ffmpeg
// never keeps burning CPU and locking the project's files.
//
// The child is created suspended, joins the job, then resumes: it cannot
// start a grandchild (a scoop shim → the real ffmpeg) outside the job first.

var (
	modKernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = modKernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = modKernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = modKernel32.NewProc("AssignProcessToJobObject")
	procIsProcessInJob           = modKernel32.NewProc("IsProcessInJob")
	modNtdll                     = syscall.NewLazyDLL("ntdll.dll")
	procNtResumeProcess          = modNtdll.NewProc("NtResumeProcess")
)

const (
	createSuspended                   = 0x00000004
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
)

// jobBasicLimit / jobExtendedLimit mirror JOBOBJECT_BASIC_LIMIT_INFORMATION and
// JOBOBJECT_EXTENDED_LIMIT_INFORMATION (natural alignment matches on amd64/arm64;
// a size mismatch elsewhere just makes SetInformationJobObject fail → no job).
type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimit struct {
	BasicLimitInformation jobBasicLimit
	IoInfo                [6]uint64 // IO_COUNTERS
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

var (
	jobOnce sync.Once
	jobH    syscall.Handle
)

// appJob is the process-wide kill-on-close job (0 when unavailable). Its
// handle is never closed and not inheritable: it lives exactly as long as the app.
func appJob() syscall.Handle {
	jobOnce.Do(func() {
		h, err := newKillJob()
		if err == nil {
			jobH = h
		}
	})
	return jobH
}

func newKillJob() (syscall.Handle, error) {
	if err := procCreateJobObjectW.Find(); err != nil {
		return 0, err
	}
	r, _, e := procCreateJobObjectW.Call(0, 0)
	if r == 0 {
		return 0, e
	}
	h := syscall.Handle(r)
	var info jobExtendedLimit
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	ok, _, e := procSetInformationJobObject.Call(uintptr(h), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if ok == 0 {
		_ = syscall.CloseHandle(h)
		return 0, e
	}
	return h, nil
}

// start launches c bound to the app's lifetime (see the file comment). When
// the job or NtResumeProcess is unavailable it is a plain c.Start().
func start(c *exec.Cmd) error {
	job := appJob()
	if job == 0 || procNtResumeProcess.Find() != nil || procAssignProcessToJobObject.Find() != nil {
		return c.Start()
	}
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	flags := c.SysProcAttr.CreationFlags
	c.SysProcAttr.CreationFlags = flags | createSuspended
	err := c.Start()
	c.SysProcAttr.CreationFlags = flags
	if err != nil {
		return err
	}
	var resumeErr error
	if werr := c.Process.WithHandle(func(h uintptr) {
		// Best effort: a child that cannot join (a restrictive outer job)
		// still runs, just without the kill-on-exit guarantee.
		_, _, _ = procAssignProcessToJobObject.Call(uintptr(job), h)
		if flags&createSuspended == 0 {
			if st, _, _ := procNtResumeProcess.Call(h); st != 0 {
				resumeErr = fmt.Errorf("NtResumeProcess 0x%08x", uint32(st))
			}
		}
	}); werr != nil {
		return nil // already gone (killed by its context); Wait reports it
	}
	if resumeErr != nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		return fmt.Errorf("не вдалося запустити %s: %w", c.Path, resumeErr)
	}
	return nil
}

// inAppJob reports whether the process handle h belongs to the app's job (tests).
func inAppJob(h uintptr) bool {
	job := appJob()
	if job == 0 || procIsProcessInJob.Find() != nil {
		return false
	}
	var in int32
	r, _, _ := procIsProcessInJob.Call(h, uintptr(job), uintptr(unsafe.Pointer(&in)))
	return r != 0 && in != 0
}
