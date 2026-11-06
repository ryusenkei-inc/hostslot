//go:build windows

package platform

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// ---- Metrics: GlobalMemoryStatusEx ----

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

type nativeMetrics struct{}

func (nativeMetrics) Memory() (Memory, error) {
	var s memoryStatusEx
	s.Length = uint32(unsafe.Sizeof(s))
	r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return Memory{}, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	return Memory{TotalMB: s.TotalPhys / mib, FreeMB: s.AvailPhys / mib}, nil
}

func (nativeMetrics) CPUs() int { return runtime.NumCPU() }

// ---- Process tracking: Job Objects ----

type nativeTracker struct{}

func (nativeTracker) Capabilities() Capabilities {
	return Capabilities{TreeMemory: true, Method: "job object (peak committed memory)"}
}

func (nativeTracker) Start(cmd *exec.Cmd, o StartOptions) (Process, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	if err := setJobLimits(job, o); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// Go cannot start a process suspended and resume it, so children spawned
	// in the first instant before assignment may escape the job.
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, h)
		windows.CloseHandle(h)
	}
	p := &jobProcess{cmd: cmd, job: job}
	if err != nil {
		// Still run the command; only tree tracking is lost.
		windows.CloseHandle(job)
		p.job = 0
	}
	return p, nil
}

// setJobLimits applies to every process in the job: a lower priority class
// for background jobs, and kill-on-close, so closing the job after the root
// exits stops what it left behind.
func setJobLimits(job windows.Handle, o StartOptions) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if o.Background {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PRIORITY_CLASS
		info.BasicLimitInformation.PriorityClass = windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	if o.Cleanup {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	}
	if info.BasicLimitInformation.LimitFlags == 0 {
		return nil
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return nil
}

type jobProcess struct {
	cmd  *exec.Cmd
	mu   sync.Mutex
	job  windows.Handle
	peak uint64
}

func (p *jobProcess) Pid() int { return p.cmd.Process.Pid }

func (p *jobProcess) Wait() error {
	err := p.cmd.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peak = max(p.peak, p.queryLocked())
	if p.job != 0 {
		windows.CloseHandle(p.job)
		p.job = 0
	}
	return err
}

func (p *jobProcess) PeakMB() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(p.peak, p.queryLocked())
}

// jobObjectMemoryUsageInformation is JobObjectMemoryUsageInformation
// (Windows 10+), missing from x/sys/windows.
const jobObjectMemoryUsageInformation = 28

// jobMemoryUsage is JOBOBJECT_MEMORY_USAGE_INFORMATION.
type jobMemoryUsage struct {
	JobMemory         uint64
	PeakJobMemoryUsed uint64
}

func (p *jobProcess) CurrentMB() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.job == 0 {
		return 0
	}
	var info jobMemoryUsage
	if err := windows.QueryInformationJobObject(p.job, jobObjectMemoryUsageInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return 0
	}
	return info.JobMemory / mib
}

func (p *jobProcess) queryLocked() uint64 {
	if p.job == 0 {
		return 0
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return 0
	}
	return uint64(info.PeakJobMemoryUsed) / mib
}

func (p *jobProcess) KillTree() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.job != 0 {
		return windows.TerminateJobObject(p.job, 1)
	}
	return p.cmd.Process.Kill()
}

// ---- Liveness ----

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied means the process exists but belongs to someone else.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	const stillActive = 259
	return code == stillActive
}

// DetachedAttr returns process attributes for a daemon that outlives the CLI.
func DetachedAttr() *syscall.SysProcAttr {
	const detachedProcess = 0x00000008
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | detachedProcess,
		HideWindow:    true,
	}
}

// ---- IPC: named pipe ----

// Each daemon listens on its own pipe and writes the name to pipeFile.
// go-winio creates a pipe's first instance exclusively, so a new daemon
// cannot listen on a name whose instances a retired daemon still holds for
// its running jobs.
type pipeIPC struct {
	base string // also the name of daemons before pipeFile existed
	file string
}

// pipeFile names the current daemon's pipe inside the runtime directory.
const pipeFile = "daemon.pipe"

func newIPC(p Paths) IPC {
	return pipeIPC{base: `\\.\pipe\hostslot-` + instanceID(p.Data), file: filepath.Join(p.Runtime, pipeFile)}
}

func (i pipeIPC) current() string {
	b, err := os.ReadFile(i.file)
	if name := strings.TrimSpace(string(b)); err == nil && strings.HasPrefix(name, i.base) {
		return name
	}
	return i.base
}

func (i pipeIPC) Address() string { return i.current() }

func (i pipeIPC) Listen() (net.Listener, error) {
	sd, err := ownerOnlySDDL()
	if err != nil {
		return nil, err
	}
	name := i.base + "-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	l, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: sd})
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(i.file, []byte(name)); err != nil {
		l.Close()
		return nil, err
	}
	return &pipeListener{Listener: l, name: name, file: i.file}, nil
}

func (i pipeIPC) Dial(timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(i.current(), &timeout)
}

// pipeListener removes pipeFile on Close while it still names this pipe, so
// a retired daemon never removes its successor's name.
type pipeListener struct {
	net.Listener
	name string
	file string
	once sync.Once
}

func (l *pipeListener) Close() error {
	l.once.Do(func() {
		if b, err := os.ReadFile(l.file); err == nil && strings.TrimSpace(string(b)) == l.name {
			os.Remove(l.file)
		}
	})
	return l.Listener.Close()
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pipe-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func ownerOnlySDDL() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return "D:P(A;;GA;;;" + user.User.Sid.String() + ")", nil
}

// ---- Locks: named mutex ----

type mutexLocker struct{ prefix string }

func newLocker(p Paths) Locker {
	return mutexLocker{prefix: `Local\hostslot-` + instanceID(p.Data) + "-"}
}

// TryLock pins a goroutine to an OS thread for the lifetime of the lock,
// because a Windows mutex is owned by a thread, not a process. The OS marks
// the mutex abandoned, and so free, when the holding process dies.
func (l mutexLocker) TryLock(name string) (func(), bool, error) {
	type result struct {
		ok  bool
		err error
	}
	results := make(chan result)
	release := make(chan struct{})
	released := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		n, err := windows.UTF16PtrFromString(l.prefix + name)
		if err != nil {
			results <- result{err: err}
			return
		}
		h, err := windows.CreateMutex(nil, false, n)
		if h == 0 {
			results <- result{err: fmt.Errorf("CreateMutex: %w", err)}
			return
		}
		ev, err := windows.WaitForSingleObject(h, 0)
		switch ev {
		case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
		case uint32(windows.WAIT_TIMEOUT):
			windows.CloseHandle(h)
			results <- result{}
			return
		default:
			windows.CloseHandle(h)
			results <- result{err: fmt.Errorf("WaitForSingleObject: %w", err)}
			return
		}
		results <- result{ok: true}
		<-release
		windows.ReleaseMutex(h)
		windows.CloseHandle(h)
		close(released)
	}()
	r := <-results
	if !r.ok {
		return nil, false, r.err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(release)
			<-released
		})
	}, true, nil
}
