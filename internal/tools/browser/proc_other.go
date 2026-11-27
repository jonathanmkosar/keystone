//go:build !windows && !unix

package browser

import "os"

// process is not available on this system: no browser is started.
type process struct {
	in, out *os.File
	wait    chan struct{}
}

func startProcess(string, Launch) (*process, error) { return nil, ErrNoBrowser }

func (p *process) kill()    {}
func (p *process) release() {}
