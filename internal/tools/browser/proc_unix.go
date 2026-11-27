//go:build unix

package browser

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/jonathanmkosar/keystone/internal/tools/browser/guard"
)

// process is a running browser and the two ends of its DevTools pipe that
// this process holds. No other process holds them, and no port is opened.
type process struct {
	cmd  *exec.Cmd
	in   *os.File // Keystone writes, the browser reads
	out  *os.File // the browser writes, Keystone reads
	wait chan struct{}
}

// startProcess starts the browser with the command line guard.Args makes,
// checked by guard.CheckArgs. On Unix the pipe's ends are the browser's
// file descriptors 3 and 4, which --remote-debugging-pipe reads and writes.
// The browser runs in a process group of its own, which kill ends whole.
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
	args := guard.Args(l.Profile, l.Width, l.Height)
	if err := guard.CheckArgs(args, l.Profile); err != nil {
		for _, f := range []*os.File{browserIn, keystoneOut, keystoneIn, browserOut} {
			f.Close()
		}
		return nil, err
	}
	cmd := exec.Command(program, args...)
	cmd.ExtraFiles = []*os.File{browserIn, browserOut}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{browserIn, keystoneOut, keystoneIn, browserOut} {
			f.Close()
		}
		return nil, err
	}
	browserIn.Close()
	browserOut.Close()
	p := &process{cmd: cmd, in: keystoneOut, out: keystoneIn, wait: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(p.wait)
	}()
	return p, nil
}

// kill ends the browser's process group.
func (p *process) kill() {
	syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	p.cmd.Process.Kill()
}

// release closes Keystone's ends of the pipe.
func (p *process) release() {
	p.in.Close()
	p.out.Close()
}
