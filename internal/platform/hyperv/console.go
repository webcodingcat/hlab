package hyperv

import (
	"io"
	"net"

	"github.com/Microsoft/go-winio"

	"github.com/webcodingcat/hlab/internal/platform"
)

// consoleSD lets only SYSTEM, administrators and the VM worker processes
// (NT VIRTUAL MACHINE\Virtual Machines) open a VM's serial console pipe.
// A pipe Hyper-V creates itself would let everyone read the console.
const consoleSD = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;S-1-5-83-0)"

// consolePipe is the named pipe COM1 of VM name is connected to.
func consolePipe(name string) string { return `\\.\pipe\` + name + "-com1" }

// ensureConsole serves the COM1 pipe of VM name, unless it already does,
// before the VM starts: Hyper-V then connects to it as a client, and the
// console output is read and dropped.
func (p *Platform) ensureConsole(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.consoles[name]; ok {
		return nil
	}
	l, err := winio.ListenPipe(consolePipe(name), &winio.PipeConfig{SecurityDescriptor: consoleSD})
	if err != nil {
		return &platform.Error{Kind: platform.Failed, Op: "Power", Err: err}
	}
	p.consoles[name] = l
	go drain(l)
	return nil
}

// drain accepts the pipe's clients and discards what they write until the
// listener is closed.
func drain(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			_, _ = io.Copy(io.Discard, c)
			_ = c.Close()
		}()
	}
}

// closeConsole stops serving the COM1 pipe of VM name.
func (p *Platform) closeConsole(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.consoles[name]; ok {
		_ = l.Close()
		delete(p.consoles, name)
	}
}
