//go:build windows

package browser

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/jonathanmkosar/keystone/internal/tools/browser/guard"
)

// process is a running browser and the two ends of its DevTools pipe that
// this process holds. No other process holds them, and no port is opened.
type process struct {
	cmd  *exec.Cmd
	in   *os.File // Keystone writes, the browser reads
	out  *os.File // the browser writes, Keystone reads
	job  windows.Handle
	wait chan struct{}
}

// startProcess starts the browser with the command line guard.Args makes,
// checked by guard.CheckArgs. On Windows the pipe's ends are handles the
// browser inherits, named by --remote-debugging-io-pipes, and passed to it
// alone (the handle list of the new process). The browser and everything
// it starts are in a job object that kills them when Keystone's handle to
// it closes, so a browser never outlives Keystone.
func startProcess(program string, l Launch) (*process, error) {
	browserIn, keystoneOut, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	keystoneIn, browserOut, err := os.Pipe()
	if err != nil {
		browserIn.Close()
		keystoneOut.Close()
		return nil, err
	}
	closeAll := func() {
		for _, f := range []*os.File{browserIn, keystoneOut, keystoneIn, browserOut} {
			f.Close()
		}
	}
	hIn, hOut := syscall.Handle(browserIn.Fd()), syscall.Handle(browserOut.Fd())
	for _, h := range []syscall.Handle{hIn, hOut} {
		if err := syscall.SetHandleInformation(h, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
			closeAll()
			return nil, err
		}
	}
	args := guard.Args(l.Profile, l.Width, l.Height, fmt.Sprintf("--remote-debugging-io-pipes=%d,%d", uintptr(hIn), uintptr(hOut)))
	if err := guard.CheckArgs(args, l.Profile); err != nil {
		closeAll()
		return nil, err
	}
	job, err := killOnCloseJob()
	if err != nil {
		closeAll()
		return nil, err
	}
	cmd := exec.Command(program, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{hIn, hOut}, HideWindow: true}
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		closeAll()
		return nil, err
	}
	browserIn.Close()
	browserOut.Close()
	if ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
		windows.AssignProcessToJobObject(job, ph)
		windows.CloseHandle(ph)
	}
	p := &process{cmd: cmd, in: keystoneOut, out: keystoneIn, job: job, wait: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(p.wait)
	}()
	return p, nil
}

func killOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// kill ends the browser and everything it started.
func (p *process) kill() {
	windows.TerminateJobObject(p.job, 1)
	p.cmd.Process.Kill()
}

// release closes Keystone's ends of the pipe and its handle to the job,
// which ends anything of the browser's still running.
func (p *process) release() {
	p.in.Close()
	p.out.Close()
	windows.CloseHandle(p.job)
}
