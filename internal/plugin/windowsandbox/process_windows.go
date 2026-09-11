//go:build windows && (amd64 || arm64)

// Package windowsandbox launches native Windows processes with a network-less
// AppContainer access token. AppContainer here is a Windows security primitive;
// no Docker, image, VM, or container daemon is involved.
package windowsandbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"golang.org/x/sys/windows"
)

var userenv = windows.NewLazySystemDLL("userenv.dll")
var createProfile = userenv.NewProc("CreateAppContainerProfile")
var deleteProfile = userenv.NewProc("DeleteAppContainerProfile")

type Config struct {
	Command     []string
	Directory   string
	Environment []string
	ReadPaths   []string
	ConfigRead  []string
	Stderr      io.Writer
	OnDenied    func(map[string]any)
}
type Process struct {
	Conn         net.Conn
	PID          uint32
	SID          string
	AuditError   error
	profile      *uint16
	sid          *windows.SID
	job, process windows.Handle
	audit        *auditSession
	grants       map[string]struct{}
	configRead   []string
	mu           sync.Mutex
	closed       bool
	stderr       io.Closer
	once         sync.Once
}
type securityCapabilities struct {
	SID             *windows.SID
	Capabilities    *windows.SIDAndAttributes
	Count, Reserved uint32
}

func Start(config Config) (p *Process, err error) {
	if len(config.Command) == 0 {
		return nil, fmt.Errorf("no-network Windows plugin requires runtime.command")
	}
	if config.OnDenied == nil {
		config.OnDenied = func(map[string]any) {}
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	p = &Process{grants: make(map[string]struct{}), configRead: config.ConfigRead}
	p.profile, _ = windows.UTF16PtrFromString("WeKnora.Plugin." + hex.EncodeToString(random[:]))
	defer func() {
		if err != nil {
			_ = p.Close()
			p = nil
		}
	}()
	r, _, _ := createProfile.Call(uintptr(unsafe.Pointer(p.profile)), uintptr(unsafe.Pointer(p.profile)), uintptr(unsafe.Pointer(p.profile)), 0, 0, uintptr(unsafe.Pointer(&p.sid)))
	if r != 0 {
		err = fmt.Errorf("create Windows restricted identity: HRESULT 0x%x", r)
		return
	}
	p.SID = p.sid.String()
	// Supplementary auditing may require elevated WFP permissions. Network
	// isolation is enforced by the access token independently of this observer.
	p.audit, p.AuditError = startAudit(p.SID, config.OnDenied)
	for _, path := range append([]string{config.Directory}, config.ReadPaths...) {
		if err = p.GrantRead(path); err != nil {
			return
		}
	}

	stdinR, stdinW, e := os.Pipe()
	if e != nil {
		err = e
		return
	}
	defer stdinR.Close()
	stdoutR, stdoutW, e := os.Pipe()
	if e != nil {
		stdinW.Close()
		err = e
		return
	}
	defer stdoutW.Close()
	p.Conn = &transport.PipeConn{Reader: stdoutR, Writer: stdinW}
	stderrR, stderrW, e := os.Pipe()
	if e != nil {
		err = e
		return
	}
	defer stderrW.Close()
	p.stderr = stderrR
	if config.Stderr == nil {
		config.Stderr = io.Discard
	}
	go func() { _, _ = io.Copy(config.Stderr, stderrR) }()
	handles := []windows.Handle{windows.Handle(stdinR.Fd()), windows.Handle(stdoutW.Fd()), windows.Handle(stderrW.Fd())}
	for _, h := range handles {
		if err = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return
		}
	}
	attributes, e := windows.NewProcThreadAttributeList(2)
	if e != nil {
		err = e
		return
	}
	defer attributes.Delete()
	capabilities := securityCapabilities{SID: p.sid} // ZERO network capabilities.
	if err = attributes.Update(0x00020009, unsafe.Pointer(&capabilities), unsafe.Sizeof(capabilities)); err != nil {
		return
	}
	if err = attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return
	}
	si := windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = handles[0], handles[1], handles[2]
	si.ProcThreadAttributeList = attributes.List()
	args := make([]string, len(config.Command))
	for i, arg := range config.Command {
		args[i] = syscall.EscapeArg(arg)
	}
	command, e := windows.UTF16PtrFromString(strings.Join(args, " "))
	if e != nil {
		err = e
		return
	}
	exe, e := windows.UTF16PtrFromString(config.Command[0])
	if e != nil {
		err = e
		return
	}
	dir, e := windows.UTF16PtrFromString(config.Directory)
	if e != nil {
		err = e
		return
	}
	// UTF16FromString rejects embedded NULs, so build the environment explicitly.
	var env []uint16
	for _, entry := range config.Environment {
		encoded, e := windows.UTF16FromString(entry)
		if e != nil {
			err = e
			return
		}
		env = append(env, encoded...)
	}
	env = append(env, 0)
	if len(env) == 1 {
		env = append(env, 0)
	}
	p.job, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		return
	}
	var info windows.ProcessInformation
	err = windows.CreateProcess(exe, command, nil, nil, true, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW|windows.CREATE_SUSPENDED, &env[0], dir, &si.StartupInfo, &info)
	runtime.KeepAlive(capabilities)
	runtime.KeepAlive(handles)
	runtime.KeepAlive(env)
	if err != nil {
		err = fmt.Errorf("create network-less Windows process: %w", err)
		return
	}
	p.process, p.PID = info.Process, info.ProcessId
	defer windows.CloseHandle(info.Thread)
	// Verify the kernel-created token before the first plugin instruction.
	var token windows.Token
	if err = windows.OpenProcessToken(info.Process, windows.TOKEN_QUERY, &token); err != nil {
		return
	}
	var restricted uint32
	var size uint32
	const tokenIsAppContainer = 29 // TOKEN_INFORMATION_CLASS, Windows 8+
	err = windows.GetTokenInformation(token, tokenIsAppContainer, (*byte)(unsafe.Pointer(&restricted)), uint32(unsafe.Sizeof(restricted)), &size)
	_ = token.Close()
	if err != nil {
		return
	}
	if restricted != 1 {
		err = fmt.Errorf("Windows did not apply the restricted plugin identity")
		return
	}
	if err = windows.AssignProcessToJobObject(p.job, info.Process); err != nil {
		return
	}
	_, err = windows.ResumeThread(info.Thread)
	return
}

// GrantRead adds only the unique per-launch SID, preserving existing ACLs.
// UNC/network drives are forbidden: granting a path must not proxy networking
// through the trusted host. Inheritance covers files added between syncs.
func (p *Process) GrantRead(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return os.ErrClosed
	}
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return fmt.Errorf("no-network plugin cannot read a network path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs) + `\`
	v, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return err
	}
	if windows.GetDriveType(v) == windows.DRIVE_REMOTE {
		return fmt.Errorf("no-network plugin cannot read a network drive")
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	if strings.HasPrefix(abs, `\\`) {
		return fmt.Errorf("read path resolves to a network path")
	}
	v, _ = windows.UTF16PtrFromString(filepath.VolumeName(abs) + `\`)
	if windows.GetDriveType(v) == windows.DRIVE_REMOTE {
		return fmt.Errorf("read path resolves to a network drive")
	}
	if _, ok := p.grants[abs]; ok {
		return nil
	}
	if err := p.editACL(abs, windows.GRANT_ACCESS); err != nil {
		return fmt.Errorf("grant plugin read access to %s: %w", abs, err)
	}
	p.grants[abs] = struct{}{}
	return nil
}
func (p *Process) editACL(path string, mode windows.ACCESS_MODE) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE, AccessMode: mode, Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN, TrusteeValue: windows.TrusteeValueFromSID(p.sid)}}}, dacl)
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(sd)
	runtime.KeepAlive(acl)
	return err
}

// Called by trusted gRPC interceptors before the configuration is sent across
// the pipe. Only manifest-declared configuration fields grant data access.
func (p *Process) PrepareConfig(raw []byte) error {
	if len(p.configRead) == 0 {
		return nil
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	for _, field := range p.configRead {
		var value any = config
		for _, part := range strings.Split(field, ".") {
			m, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("read permission config:%s is missing", field)
			}
			value = m[part]
		}
		path, ok := value.(string)
		if !ok || !filepath.IsAbs(path) {
			return fmt.Errorf("read permission config:%s requires an absolute local path", field)
		}
		if err := p.GrantRead(path); err != nil {
			return err
		}
	}
	return nil
}

func (p *Process) Close() error {
	var result error
	p.once.Do(func() {
		// Stop the whole process tree before releasing policy or removing grants.
		if p.job != 0 {
			_ = windows.TerminateJobObject(p.job, 1)
			_ = windows.CloseHandle(p.job)
		}
		if p.process != 0 {
			_ = windows.TerminateProcess(p.process, 1)
			_, _ = windows.WaitForSingleObject(p.process, 5000)
			_ = windows.CloseHandle(p.process)
		}
		if p.Conn != nil {
			_ = p.Conn.Close()
		}
		if p.stderr != nil {
			_ = p.stderr.Close()
		}
		if p.audit != nil {
			_ = p.audit.Close()
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.closed = true
		for path := range p.grants {
			result = errors.Join(result, p.editACL(path, windows.REVOKE_ACCESS))
		}
		if p.sid != nil {
			r, _, _ := deleteProfile.Call(uintptr(unsafe.Pointer(p.profile)))
			if r != 0 {
				result = errors.Join(result, fmt.Errorf("delete plugin identity: 0x%x", r))
			}
			_ = windows.FreeSid(p.sid)
		}
	})
	return result
}
