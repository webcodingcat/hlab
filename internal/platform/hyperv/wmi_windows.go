package hyperv

// Raw WMI access through the IWbemServices and IWbemClassObject vtables
// (EXPERIMENTS.md E21). The WbemScripting automation layer turns a uint8[]
// into a SAFEARRAY of VARIANTs and costs about 330 ms per console frame;
// the raw interfaces hand out VT_ARRAY|VT_UI1, copied in one step.
//
// A connection is bound to the goroutine that opened it: openWMI locks it
// to its OS thread and initializes COM there, and every call on the
// connection and its objects, Close included, must run on that goroutine.

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modOle32    = windows.NewLazySystemDLL("ole32.dll")
	modOleAut32 = windows.NewLazySystemDLL("oleaut32.dll")

	procCoCreateInstance      = modOle32.NewProc("CoCreateInstance")
	procCoSetProxyBlanket     = modOle32.NewProc("CoSetProxyBlanket")
	procSysAllocString        = modOleAut32.NewProc("SysAllocString")
	procSysFreeString         = modOleAut32.NewProc("SysFreeString")
	procSysStringLen          = modOleAut32.NewProc("SysStringLen")
	procVariantClear          = modOleAut32.NewProc("VariantClear")
	procSafeArrayCreateVector = modOleAut32.NewProc("SafeArrayCreateVector")
	procSafeArrayDestroy      = modOleAut32.NewProc("SafeArrayDestroy")
	procSafeArrayGetLBound    = modOleAut32.NewProc("SafeArrayGetLBound")
	procSafeArrayGetUBound    = modOleAut32.NewProc("SafeArrayGetUBound")
	procSafeArrayAccessData   = modOleAut32.NewProc("SafeArrayAccessData")
	procSafeArrayUnaccessData = modOleAut32.NewProc("SafeArrayUnaccessData")
	clsidWbemLocator          = windows.GUID{Data1: 0x4590F811, Data2: 0x1D3A, Data3: 0x11D0, Data4: [8]byte{0x89, 0x1F, 0x00, 0xAA, 0x00, 0x4B, 0x2E, 0x24}}
	iidIWbemLocator           = windows.GUID{Data1: 0xDC12A687, Data2: 0x737F, Data3: 0x11CF, Data4: [8]byte{0x88, 0x4D, 0x00, 0xAA, 0x00, 0x4B, 0x2E, 0x24}}
	errSysAllocString         = errors.New("wmi: SysAllocString failed")
	errSafeArrayCreateVector  = errors.New("wmi: SafeArrayCreateVector failed")
	sFalse                    = syscall.Errno(1) // S_FALSE: COM was already initialized on this thread
)

// Vtable indexes (E21; IUnknown takes 0-2).
const (
	vtblRelease = 2

	locatorConnectServer = 3

	servicesGetObject  = 6
	servicesExecQuery  = 20
	servicesExecMethod = 24

	enumNext = 4

	objectGet           = 4
	objectPut           = 5
	objectSpawnInstance = 15
	objectGetMethod     = 19
)

const (
	clsctxInprocServer   = 1
	rpcCAuthnWinNT       = 10
	rpcCAuthnLevelCall   = 3
	rpcCImpLevelImperson = 3

	wbemFlagForwardOnlyReturnImmediately = 0x20 | 0x10
	wbemInfinite                         = 0xFFFFFFFF

	vtEmpty = 0
	vtNull  = 1
	vtI4    = 3
	vtBSTR  = 8
	vtBool  = 11
	vtUI1   = 17
	vtArray = 0x2000
)

// comObject is a COM interface pointer: its first word is the vtable.
type comObject struct{ vtbl *[64]uintptr }

// call invokes vtable entry idx with the object as first argument and
// returns the HRESULT. uintptrescapes keeps memory behind
// uintptr(unsafe.Pointer(...)) arguments alive and in place.
//
//go:uintptrescapes
func (o *comObject) call(idx int, args ...uintptr) uintptr {
	all := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[idx], all...)
	return r
}

func (o *comObject) release() {
	if o != nil {
		o.call(vtblRelease)
	}
}

// hresultError is a failed COM call.
type hresultError struct {
	op string
	hr uint32
}

func (e *hresultError) Error() string { return fmt.Sprintf("wmi %s: HRESULT 0x%08X", e.op, e.hr) }

func check(op string, r uintptr) error {
	if int32(uint32(r)) < 0 {
		return &hresultError{op: op, hr: uint32(r)}
	}
	return nil
}

// wmiConn is an IWbemServices connection to one namespace.
type wmiConn struct{ svc *comObject }

//lint:ignore U1000 OpenConsole (plan 23-06) calls it.
//
// openWMI locks the calling goroutine to its OS thread, initializes COM
// there and connects to namespace ns, for example root\virtualization\v2.
func openWMI(ns string) (*wmiConn, error) {
	runtime.LockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil && !errors.Is(err, sFalse) {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("wmi CoInitializeEx: %w", err)
	}
	svc, err := connectServer(ns)
	if err != nil {
		windows.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, err
	}
	return &wmiConn{svc: svc}, nil
}

func connectServer(ns string) (*comObject, error) {
	var loc *comObject
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidWbemLocator)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIWbemLocator)), uintptr(unsafe.Pointer(&loc)))
	if err := check("CoCreateInstance", r); err != nil {
		return nil, err
	}
	defer loc.release()
	path, err := sysAllocString(ns)
	if err != nil {
		return nil, err
	}
	defer sysFreeString(path)
	var svc *comObject
	if err := check("ConnectServer", loc.call(locatorConnectServer, path, 0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&svc)))); err != nil {
		return nil, err
	}
	r, _, _ = procCoSetProxyBlanket.Call(uintptr(unsafe.Pointer(svc)), rpcCAuthnWinNT, 0, 0,
		rpcCAuthnLevelCall, rpcCImpLevelImperson, 0, 0)
	if err := check("CoSetProxyBlanket", r); err != nil {
		svc.release()
		return nil, err
	}
	return svc, nil
}

// Close releases the connection, uninitializes COM and unlocks the thread.
func (c *wmiConn) Close() {
	c.svc.release()
	c.svc = nil
	windows.CoUninitialize()
	runtime.UnlockOSThread()
}

// Query runs a WQL query and returns all objects; the caller releases them.
func (c *wmiConn) Query(wql string) ([]*wmiObject, error) {
	lang, err := sysAllocString("WQL")
	if err != nil {
		return nil, err
	}
	defer sysFreeString(lang)
	q, err := sysAllocString(wql)
	if err != nil {
		return nil, err
	}
	defer sysFreeString(q)
	var enum *comObject
	if err := check("ExecQuery", c.svc.call(servicesExecQuery, lang, q, wbemFlagForwardOnlyReturnImmediately, 0, uintptr(unsafe.Pointer(&enum)))); err != nil {
		return nil, err
	}
	defer enum.release()
	var out []*wmiObject
	for {
		var obj *comObject
		var n uint32
		if err := check("Next", enum.call(enumNext, wbemInfinite, 1, uintptr(unsafe.Pointer(&obj)), uintptr(unsafe.Pointer(&n)))); err != nil {
			for _, o := range out {
				o.Release()
			}
			return nil, err
		}
		if n == 0 {
			return out, nil
		}
		out = append(out, &wmiObject{o: obj})
	}
}

// GetObject returns the class or instance at path; the caller releases it.
func (c *wmiConn) GetObject(path string) (*wmiObject, error) {
	p, err := sysAllocString(path)
	if err != nil {
		return nil, err
	}
	defer sysFreeString(p)
	var obj *comObject
	if err := check("GetObject "+path, c.svc.call(servicesGetObject, p, 0, 0, uintptr(unsafe.Pointer(&obj)), 0)); err != nil {
		return nil, err
	}
	return &wmiObject{o: obj}, nil
}

// ExecMethod calls method on the object at path with the in-parameters in
// (nil for none) and returns the out-parameters; the caller releases them.
func (c *wmiConn) ExecMethod(path, method string, in *wmiObject) (*wmiObject, error) {
	p, err := sysAllocString(path)
	if err != nil {
		return nil, err
	}
	defer sysFreeString(p)
	m, err := sysAllocString(method)
	if err != nil {
		return nil, err
	}
	defer sysFreeString(m)
	var inPtr uintptr
	if in != nil {
		inPtr = uintptr(unsafe.Pointer(in.o))
	}
	var out *comObject
	if err := check("ExecMethod "+method, c.svc.call(servicesExecMethod, p, m, 0, 0, inPtr, uintptr(unsafe.Pointer(&out)), 0)); err != nil {
		return nil, err
	}
	return &wmiObject{o: out}, nil
}

// wmiObject is an IWbemClassObject: a class, an instance or parameters.
type wmiObject struct{ o *comObject }

// Release frees the object; it may be called on nil.
func (w *wmiObject) Release() {
	if w != nil {
		w.o.release()
		w.o = nil
	}
}

// Get reads property name: string (BSTR), int (I4, which WMI also uses
// for uint32), bool, []byte (VT_ARRAY|VT_UI1) or nil (empty or null).
func (w *wmiObject) Get(name string) (any, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var v variant
	if err := check("Get "+name, w.o.call(objectGet, uintptr(unsafe.Pointer(n)), 0, uintptr(unsafe.Pointer(&v)), 0, 0)); err != nil {
		return nil, err
	}
	defer variantClear(&v)
	return v.value()
}

// Put sets property name from a string, int, bool or []byte.
func (w *wmiObject) Put(name string, val any) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var v variant
	if err := v.set(val); err != nil {
		return fmt.Errorf("wmi Put %s: %w", name, err)
	}
	defer variantClear(&v)
	return check("Put "+name, w.o.call(objectPut, uintptr(unsafe.Pointer(n)), 0, uintptr(unsafe.Pointer(&v)), 0))
}

// SpawnInstance creates an instance of a class or method signature; the
// caller releases it.
func (w *wmiObject) SpawnInstance() (*wmiObject, error) {
	var obj *comObject
	if err := check("SpawnInstance", w.o.call(objectSpawnInstance, 0, uintptr(unsafe.Pointer(&obj)))); err != nil {
		return nil, err
	}
	return &wmiObject{o: obj}, nil
}

// GetMethod returns the in-parameter signature of method on a class, nil
// when it takes none; the caller releases it.
func (w *wmiObject) GetMethod(method string) (*wmiObject, error) {
	n, err := windows.UTF16PtrFromString(method)
	if err != nil {
		return nil, err
	}
	var in, out *comObject
	if err := check("GetMethod "+method, w.o.call(objectGetMethod, uintptr(unsafe.Pointer(n)), 0, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Pointer(&out)))); err != nil {
		return nil, err
	}
	out.release()
	if in == nil {
		return nil, nil
	}
	return &wmiObject{o: in}, nil
}

// variant is the 24-byte VARIANT of 64-bit Windows.
type variant struct {
	vt  uint16
	_   [3]uint16
	val [2]uint64
}

func (v *variant) ptr() unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&v.val[0])) }

func (v *variant) setPtr(p uintptr) { *(*uintptr)(unsafe.Pointer(&v.val[0])) = p }

func (v *variant) value() (any, error) {
	switch v.vt {
	case vtEmpty, vtNull:
		return nil, nil
	case vtBSTR:
		p := v.ptr()
		if p == nil {
			return "", nil
		}
		n, _, _ := procSysStringLen.Call(uintptr(p))
		return windows.UTF16ToString(unsafe.Slice((*uint16)(p), n)), nil
	case vtI4:
		return int(*(*int32)(unsafe.Pointer(&v.val[0]))), nil
	case vtBool:
		return *(*int16)(unsafe.Pointer(&v.val[0])) != 0, nil
	case vtArray | vtUI1:
		return safeArrayBytes(v.ptr())
	default:
		return nil, fmt.Errorf("wmi: unsupported VARIANT type 0x%X", v.vt)
	}
}

func (v *variant) set(val any) error {
	switch x := val.(type) {
	case string:
		b, err := sysAllocString(x)
		if err != nil {
			return err
		}
		v.vt = vtBSTR
		v.setPtr(b)
	case int:
		v.vt = vtI4
		*(*int32)(unsafe.Pointer(&v.val[0])) = int32(x)
	case bool:
		v.vt = vtBool
		if x {
			*(*int16)(unsafe.Pointer(&v.val[0])) = -1 // VARIANT_TRUE
		}
	case []byte:
		psa, err := safeArrayFromBytes(x)
		if err != nil {
			return err
		}
		v.vt = vtArray | vtUI1
		v.setPtr(psa)
	default:
		return fmt.Errorf("unsupported type %T", val)
	}
	return nil
}

func variantClear(v *variant) { _, _, _ = procVariantClear.Call(uintptr(unsafe.Pointer(v))) }

func sysAllocString(s string) (uintptr, error) {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return 0, err
	}
	b, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(p)))
	if b == 0 {
		return 0, errSysAllocString
	}
	return b, nil
}

func sysFreeString(b uintptr) { _, _, _ = procSysFreeString.Call(b) }

// safeArrayFromBytes copies b into a new one-dimensional VT_UI1 SAFEARRAY.
func safeArrayFromBytes(b []byte) (uintptr, error) {
	psa, _, _ := procSafeArrayCreateVector.Call(vtUI1, 0, uintptr(len(b)))
	if psa == 0 {
		return 0, errSafeArrayCreateVector
	}
	if len(b) == 0 {
		return psa, nil
	}
	var data unsafe.Pointer
	r, _, _ := procSafeArrayAccessData.Call(psa, uintptr(unsafe.Pointer(&data)))
	if err := check("SafeArrayAccessData", r); err != nil {
		_, _, _ = procSafeArrayDestroy.Call(psa)
		return 0, err
	}
	copy(unsafe.Slice((*byte)(data), len(b)), b)
	_, _, _ = procSafeArrayUnaccessData.Call(psa)
	return psa, nil
}

// safeArrayBytes copies a one-dimensional VT_UI1 SAFEARRAY in one step.
func safeArrayBytes(psa unsafe.Pointer) ([]byte, error) {
	if psa == nil {
		return nil, nil
	}
	var lo, hi int32
	if err := check("SafeArrayGetLBound", first(procSafeArrayGetLBound.Call(uintptr(psa), 1, uintptr(unsafe.Pointer(&lo))))); err != nil {
		return nil, err
	}
	if err := check("SafeArrayGetUBound", first(procSafeArrayGetUBound.Call(uintptr(psa), 1, uintptr(unsafe.Pointer(&hi))))); err != nil {
		return nil, err
	}
	n := int(hi) - int(lo) + 1
	if n <= 0 {
		return []byte{}, nil
	}
	var data unsafe.Pointer
	if err := check("SafeArrayAccessData", first(procSafeArrayAccessData.Call(uintptr(psa), uintptr(unsafe.Pointer(&data))))); err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, unsafe.Slice((*byte)(data), n))
	_, _, _ = procSafeArrayUnaccessData.Call(uintptr(psa))
	return out, nil
}

func first(r, _ uintptr, _ error) uintptr { return r }
