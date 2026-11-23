package hyperv

// The graphical console of a VM through raw WMI (EXPERIMENTS.md E21, E25):
// frames from GetVirtualSystemThumbnailImage, keys through
// Msvm_Keyboard.TypeScancodes, the pointer through Msvm_SyntheticMouse.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/webcodingcat/hlab/internal/platform"
)

const (
	virtNamespace = `root\virtualization\v2`

	// GetVirtualSystemThumbnailImage return values without a picture (E25):
	// off, starting, shutting down or saving; restoring.
	thumbNoPictureOff       = 32775
	thumbNoPictureRestoring = 32779

	wbemENotFound = 0x80041002
)

var (
	_ platform.Console = (*Platform)(nil)

	errConsoleClosed = errors.New("console closed")
)

// OpenConsole opens the screen, keyboard and mouse of VM vm. One worker
// goroutine owns the WMI connection, which is bound to its OS thread.
func (p *Platform) OpenConsole(ctx context.Context, vm string) (platform.ConsoleConn, error) {
	c := &screenConn{reqs: make(chan screenReq), quit: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go c.work(vm, ready)
	if err := <-ready; err != nil {
		var pe *platform.Error
		if errors.As(err, &pe) {
			return nil, err
		}
		return nil, &platform.Error{Kind: platform.Failed, Op: "OpenConsole", Err: err}
	}
	return c, nil
}

type screenReq struct {
	fn    func(w *consoleWMI) error
	reply chan error
}

// screenConn is a platform.ConsoleConn; every call runs on its worker.
type screenConn struct {
	reqs      chan screenReq
	quit      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func (c *screenConn) work(vm string, ready chan<- error) {
	defer close(c.done)
	conn, err := openWMI(virtNamespace)
	if err != nil {
		ready <- err
		return
	}
	defer conn.Close()
	w := &consoleWMI{conn: conn, paths: map[string]string{}, params: map[string]*wmiObject{}}
	defer w.release()
	if err := w.lookup(vm); err != nil {
		ready <- err
		return
	}
	ready <- nil
	for {
		select {
		case r := <-c.reqs:
			r.reply <- r.fn(w)
		case <-c.quit:
			return
		}
	}
}

// do runs fn on the worker and waits for its result.
func (c *screenConn) do(ctx context.Context, op string, fn func(w *consoleWMI) error) error {
	r := screenReq{fn: fn, reply: make(chan error, 1)}
	select {
	case c.reqs <- r:
	case <-c.quit:
		return &platform.Error{Kind: platform.Failed, Op: op, Err: errConsoleClosed}
	case <-ctx.Done():
		return &platform.Error{Kind: platform.Failed, Op: op, Err: ctx.Err()}
	}
	if err := <-r.reply; err != nil {
		return &platform.Error{Kind: platform.Failed, Op: op, Err: err}
	}
	return nil
}

func (c *screenConn) Screen(ctx context.Context) (platform.Frame, bool, error) {
	var f platform.Frame
	var ok bool
	err := c.do(ctx, "Screen", func(w *consoleWMI) error {
		var err error
		f, ok, err = w.screen()
		return err
	})
	return f, ok, err
}

func (c *screenConn) Scancodes(ctx context.Context, codes []byte) error {
	return c.do(ctx, "Scancodes", func(w *consoleWMI) error {
		return w.input("Msvm_Keyboard", "TypeScancodes", map[string]any{"Scancodes": codes})
	})
}

func (c *screenConn) MouseMove(ctx context.Context, x, y int) error {
	return c.do(ctx, "MouseMove", func(w *consoleWMI) error {
		return w.input("Msvm_SyntheticMouse", "SetAbsolutePosition", map[string]any{"HorizontalPosition": x, "VerticalPosition": y})
	})
}

func (c *screenConn) MouseButton(ctx context.Context, index int, down bool) error {
	return c.do(ctx, "MouseButton", func(w *consoleWMI) error {
		return w.input("Msvm_SyntheticMouse", "SetButtonState", map[string]any{"ButtonIndex": index, "IsDown": down})
	})
}

func (c *screenConn) MouseScroll(ctx context.Context, delta int) error {
	return c.do(ctx, "MouseScroll", func(w *consoleWMI) error {
		return w.input("Msvm_SyntheticMouse", "SetScrollPosition", map[string]any{"ScrollPositionDelta": delta})
	})
}

// Close ends the worker, which releases the WMI connection on its thread.
func (c *screenConn) Close() error {
	c.closeOnce.Do(func() { close(c.quit) })
	<-c.done
	return nil
}

// consoleWMI is the worker's state: the VM's identity, cached object paths
// and reused in-parameter objects.
type consoleWMI struct {
	conn   *wmiConn
	guid   string                // Msvm_ComputerSystem.Name
	vssd   string                // path of the realized settings
	vsms   string                // path of Msvm_VirtualSystemManagementService
	paths  map[string]string     // device class -> instance path, while it exists
	params map[string]*wmiObject // "class.method" -> in-parameters
}

func (w *consoleWMI) release() {
	for _, o := range w.params {
		o.Release()
	}
}

// lookup finds the VM by name; the realized settings and the management
// service exist in every VM state.
func (w *consoleWMI) lookup(vm string) error {
	sys, err := w.queryOne(fmt.Sprintf("SELECT Name FROM Msvm_ComputerSystem WHERE ElementName='%s'", wqlString(vm)), "Name")
	if err != nil {
		return err
	}
	if sys == "" {
		return &platform.Error{Kind: platform.NotFound, Op: "OpenConsole", Err: fmt.Errorf("vm %s not found", vm)}
	}
	w.guid = sys
	w.vssd, err = w.queryOne(fmt.Sprintf("SELECT * FROM Msvm_VirtualSystemSettingData WHERE VirtualSystemIdentifier='%s' AND VirtualSystemType='Microsoft:Hyper-V:System:Realized'", wqlString(sys)), "__PATH")
	if err != nil {
		return err
	}
	if w.vssd == "" {
		return fmt.Errorf("vm %s: no realized settings", vm)
	}
	w.vsms, err = w.queryOne("SELECT * FROM Msvm_VirtualSystemManagementService", "__PATH")
	if err != nil {
		return err
	}
	if w.vsms == "" {
		return errors.New("no Msvm_VirtualSystemManagementService")
	}
	return nil
}

// queryOne returns property prop of the first object of wql, "" for none.
func (w *consoleWMI) queryOne(wql, prop string) (string, error) {
	objs, err := w.conn.Query(wql)
	if err != nil {
		return "", err
	}
	defer releaseAll(objs)
	if len(objs) == 0 {
		return "", nil
	}
	v, err := objs[0].Get(prop)
	if err != nil {
		return "", err
	}
	s, _ := v.(string)
	return s, nil
}

// screen reads the current resolution and grabs the screen at that size.
func (w *consoleWMI) screen() (platform.Frame, bool, error) {
	width, height, err := w.resolution()
	if err != nil || width <= 0 || height <= 0 {
		return platform.Frame{}, false, err
	}
	out, err := w.exec(w.vsms, "Msvm_VirtualSystemManagementService", "GetVirtualSystemThumbnailImage",
		map[string]any{"TargetSystem": w.vssd, "WidthPixels": width, "HeightPixels": height})
	if err != nil {
		return platform.Frame{}, false, err
	}
	defer out.Release()
	rv, err := returnValue(out)
	if err != nil {
		return platform.Frame{}, false, err
	}
	switch rv {
	case 0:
	case thumbNoPictureOff, thumbNoPictureRestoring:
		return platform.Frame{}, false, nil
	default:
		return platform.Frame{}, false, fmt.Errorf("GetVirtualSystemThumbnailImage: return value %d", rv)
	}
	v, err := out.Get("ImageData")
	if err != nil {
		return platform.Frame{}, false, err
	}
	img, _ := v.([]byte)
	n := width * height * 2
	if len(img) < n {
		// The resolution changed between the two calls.
		return platform.Frame{}, false, nil
	}
	return platform.Frame{Width: width, Height: height, Pix: img[:n]}, true, nil
}

// resolution returns the size of the VM's video head, 0 while it has none.
func (w *consoleWMI) resolution() (int, int, error) {
	objs, err := w.conn.Query(fmt.Sprintf("SELECT CurrentHorizontalResolution, CurrentVerticalResolution FROM Msvm_VideoHead WHERE SystemName='%s'", wqlString(w.guid)))
	if err != nil {
		return 0, 0, err
	}
	defer releaseAll(objs)
	for _, o := range objs {
		h, err := o.Get("CurrentHorizontalResolution")
		if err != nil {
			return 0, 0, err
		}
		v, err := o.Get("CurrentVerticalResolution")
		if err != nil {
			return 0, 0, err
		}
		hi, _ := h.(int)
		vi, _ := v.(int)
		if hi > 0 && vi > 0 {
			return hi, vi, nil
		}
	}
	return 0, 0, nil
}

// input calls method on the VM's device of class. The devices exist only
// while the VM runs: without one, or when it refuses the input (non-zero
// return value), the input is dropped and the console stays open.
func (w *consoleWMI) input(class, method string, args map[string]any) error {
	path := w.paths[class]
	if path == "" {
		var err error
		path, err = w.queryOne(fmt.Sprintf("SELECT * FROM %s WHERE SystemName='%s'", class, wqlString(w.guid)), "__PATH")
		if err != nil || path == "" {
			return err
		}
		w.paths[class] = path
	}
	out, err := w.exec(path, class, method, args)
	if err != nil {
		var he *hresultError
		if errors.As(err, &he) && he.hr == wbemENotFound {
			delete(w.paths, class)
			return nil
		}
		return err
	}
	out.Release()
	return nil
}

// exec calls method on the object at path with args, reusing one
// in-parameter object per method; the caller releases the result.
func (w *consoleWMI) exec(path, class, method string, args map[string]any) (*wmiObject, error) {
	key := class + "." + method
	in := w.params[key]
	if in == nil {
		cls, err := w.conn.GetObject(class)
		if err != nil {
			return nil, err
		}
		sig, err := cls.GetMethod(method)
		cls.Release()
		if err != nil {
			return nil, err
		}
		if sig == nil {
			return nil, fmt.Errorf("%s: no in-parameters", key)
		}
		in, err = sig.SpawnInstance()
		sig.Release()
		if err != nil {
			return nil, err
		}
		w.params[key] = in
	}
	for name, val := range args {
		if err := in.Put(name, val); err != nil {
			return nil, err
		}
	}
	return w.conn.ExecMethod(path, method, in)
}

func returnValue(out *wmiObject) (int, error) {
	v, err := out.Get("ReturnValue")
	if err != nil {
		return 0, err
	}
	rv, _ := v.(int)
	return rv, nil
}

func releaseAll(objs []*wmiObject) {
	for _, o := range objs {
		o.Release()
	}
}

// wqlString escapes s for a single-quoted WQL string literal.
func wqlString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}
