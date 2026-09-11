//go:build windows && (amd64 || arm64)

package windowsandbox

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var wfp = windows.NewLazySystemDLL("fwpuclnt.dll")
var engineOpen = wfp.NewProc("FwpmEngineOpen0")
var engineClose = wfp.NewProc("FwpmEngineClose0")
var engineSet = wfp.NewProc("FwpmEngineSetOption0")
var engineGet = wfp.NewProc("FwpmEngineGetOption0")
var freeWFP = wfp.NewProc("FwpmFreeMemory0")
var subscribe = wfp.NewProc("FwpmNetEventSubscribe1")
var unsubscribe = wfp.NewProc("FwpmNetEventUnsubscribe0")

// Win64 layouts of FWPM_NET_EVENT2 and FWPM_NET_EVENT_HEADER2. Version 2
// includes the package SID, so descendants are attributed by OS identity,
// rather than trusting process names or plugin-written log messages.
type byteBlob struct {
	Size uint32
	Data *byte
}
type netHeader struct {
	Time                  windows.Filetime
	Flags, IPVersion      uint32
	Protocol              uint8
	_                     [3]byte
	Local, Remote         [16]byte
	LocalPort, RemotePort uint16
	Scope                 uint32
	AppID                 byteBlob
	UserID                *windows.SID
	Family                uint32
	PackageSID            *windows.SID
}
type netEvent struct {
	Header netHeader
	Type   uint32
	Data   unsafe.Pointer
}
type eventSubscription struct {
	Template uintptr
	Flags    uint32
	Key      windows.GUID
}
type wfpValue struct {
	Type  uint32
	Value uintptr
}

type auditSession struct {
	engine, subscription windows.Handle
	sid                  string
	emit                 func(map[string]any)
	once                 sync.Once
}

var auditSessions sync.Map

// A single callback avoids leaking a Go runtime callback thunk on each restart.
var auditCallback = windows.NewCallback(func(key, raw uintptr) uintptr {
	value, ok := auditSessions.Load(key)
	if !ok || raw == 0 {
		return 0
	}
	a := value.(*auditSession)
	e := (*netEvent)(unsafe.Pointer(raw))
	// CLASSIFY_DROP=3, CAPABILITY_DROP=7. Never count allow notifications.
	if (e.Type != 3 && e.Type != 7) || e.Header.PackageSID == nil || e.Header.PackageSID.String() != a.sid {
		return 0
	}
	ip := net.IP(e.Header.Remote[:])
	if e.Header.IPVersion == 0 {
		v := binary.LittleEndian.Uint32(e.Header.Remote[:4])
		ip = net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
	details := map[string]any{"backend": "windows-wfp", "package_sid": a.sid, "remote_address": ip.String(), "remote_port": e.Header.RemotePort, "protocol": e.Header.Protocol, "event_type": e.Type}
	if b := e.Header.AppID; b.Data != nil && b.Size > 0 && b.Size <= 65536 && b.Size%2 == 0 {
		details["application"] = windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(b.Data)), int(b.Size/2)))
	}
	a.emit(details)
	return 0
})

func winResult(name string, result uintptr) error {
	if result != 0 {
		return fmt.Errorf("%s: %w", name, windows.Errno(result))
	}
	return nil
}

func startAudit(sid string, emit func(map[string]any)) (*auditSession, error) {
	a := &auditSession{sid: sid, emit: emit}
	r, _, _ := engineOpen.Call(0, 10, 0, 0, uintptr(unsafe.Pointer(&a.engine)))
	if err := winResult("open Windows network audit (administrator access required)", r); err != nil {
		return nil, err
	}
	var current *wfpValue
	r, _, _ = engineGet.Call(uintptr(a.engine), 0, uintptr(unsafe.Pointer(&current)))
	if err := winResult("read Windows network audit configuration", r); err != nil {
		a.Close()
		return nil, err
	}
	enabled := current != nil && current.Type == 3 && uint32(current.Value) != 0
	if current != nil {
		freeWFP.Call(uintptr(unsafe.Pointer(&current)))
	}
	if !enabled {
		// Enable collection only when needed; never disable a shared audit setting.
		v := wfpValue{Type: 3, Value: 1}
		r, _, _ = engineSet.Call(uintptr(a.engine), 0, uintptr(unsafe.Pointer(&v)))
		if err := winResult("enable Windows network audit", r); err != nil {
			a.Close()
			return nil, err
		}
	}
	key := uintptr(a.engine)
	auditSessions.Store(key, a)
	s := eventSubscription{}
	r, _, _ = subscribe.Call(uintptr(a.engine), uintptr(unsafe.Pointer(&s)), auditCallback, key, uintptr(unsafe.Pointer(&a.subscription)))
	if err := winResult("subscribe Windows network denials", r); err != nil {
		a.Close()
		return nil, err
	}
	return a, nil
}
func (a *auditSession) Close() error {
	a.once.Do(func() {
		if a.subscription != 0 {
			unsubscribe.Call(uintptr(a.engine), uintptr(a.subscription))
		}
		auditSessions.Delete(uintptr(a.engine))
		if a.engine != 0 {
			engineClose.Call(uintptr(a.engine))
		}
	})
	return nil
}
