//go:build windows && (amd64 || arm64)

package windowsandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type probeResult struct {
	File   string            `json:"file"`
	Denied map[string]bool   `json:"denied"`
	Errors map[string]string `json:"errors"`
	Child  *probeResult      `json:"child,omitempty"`
}

func TestWindowsNativeHelper(t *testing.T) {
	if os.Getenv("WEKNORA_NATIVE_HELPER") != "1" {
		return
	}
	result := probeResult{Denied: make(map[string]bool), Errors: make(map[string]string)}
	content, err := os.ReadFile(os.Getenv("WEKNORA_NATIVE_FILE"))
	if err != nil {
		result.File = err.Error()
	} else {
		result.File = string(content)
	}
	for label, address := range map[string]string{"tcp4": "1.1.1.1:443", "tcp6": "[2606:4700:4700::1111]:443", "loopback": os.Getenv("WEKNORA_NATIVE_LOOPBACK")} {
		c, err := net.DialTimeout("tcp", address, time.Second)
		if c != nil {
			_ = c.Close()
		}
		result.Denied[label] = errors.Is(err, syscall.Errno(10013))
		if label == "loopback" {
			result.Denied[label] = err != nil
		}
		result.Errors[label] = fmt.Sprint(err)
	}
	c, err := net.DialTimeout("udp", os.Getenv("WEKNORA_NATIVE_UDP"), time.Second)
	if err == nil {
		_, err = c.Write([]byte("weknora-blocked-probe"))
		_ = c.Close()
	}
	result.Denied["udp"] = errors.Is(err, syscall.Errno(10013))
	// Winsock may return success while WFP drops the datagram asynchronously.
	// The host checks actual packet delivery against a positive control below.
	result.Errors["udp"] = fmt.Sprint(err)
	if os.Getenv("WEKNORA_NATIVE_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsNativeHelper$")
		cmd.Env = append(os.Environ(), "WEKNORA_NATIVE_CHILD=1")
		// Reuse inherited pipes: opening NUL is forbidden inside AppContainer.
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			result.Errors["child"] = err.Error()
		} else {
			result.Child = &probeResult{}
			if err := json.Unmarshal(out, result.Child); err != nil {
				result.Errors["child"] = err.Error()
			}
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func TestWindowsNativeNoNetwork(t *testing.T) {
	if os.Getenv("WEKNORA_WINDOWS_SANDBOX_TEST") != "1" {
		t.Skip("set WEKNORA_WINDOWS_SANDBOX_TEST=1 for native OS acceptance")
	}
	dir := t.TempDir()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "probe.exe")
	if err := os.WriteFile(exe, binary, 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(data, []byte("pipe-and-file-ok"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	positive, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("TCP positive control", err)
	}
	_ = positive.Close()
	// Only claim IPv6 isolation when the host has a working IPv6 route.
	ipv6, ipv6Err := net.DialTimeout("tcp6", "[2606:4700:4700::1111]:443", 3*time.Second)
	if ipv6 != nil {
		_ = ipv6.Close()
	}
	if ipv6Err != nil {
		t.Logf("IPv6 isolation not verified: host positive control failed: %v", ipv6Err)
	}
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	positiveUDP, err := net.Dial("udp", udp.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = positiveUDP.Write([]byte("positive-control"))
	_ = positiveUDP.Close()
	if err != nil {
		t.Fatal(err)
	}
	_ = udp.SetReadDeadline(time.Now().Add(time.Second))
	var packet [128]byte
	if _, _, err = udp.ReadFrom(packet[:]); err != nil {
		t.Fatal("UDP positive control", err)
	}
	var eventsMu sync.Mutex
	var events []map[string]any
	var stderr bytes.Buffer
	p, err := Start(Config{Command: []string{exe, "-test.run=^TestWindowsNativeHelper$"}, Directory: dir, Stderr: &stderr,
		Environment: append(os.Environ(), "WEKNORA_NATIVE_HELPER=1", "WEKNORA_NATIVE_FILE="+data, "WEKNORA_NATIVE_LOOPBACK="+listener.Addr().String(), "WEKNORA_NATIVE_UDP="+udp.LocalAddr().String()),
		OnDenied:    func(event map[string]any) { eventsMu.Lock(); defer eventsMu.Unlock(); events = append(events, event) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	t.Logf("native pid=%d sid=%s audit_available=%v", p.PID, p.SID, p.AuditError == nil)
	if p.AuditError != nil {
		t.Logf("supplementary audit: %v", p.AuditError)
		if os.Getenv("WEKNORA_REQUIRE_WFP_AUDIT") == "1" {
			t.Fatal(p.AuditError)
		}
	}
	done := make(chan error, 1)
	var result probeResult
	go func() { done <- json.NewDecoder(p.Conn).Decode(&result) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read native result: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("native probe timed out")
	}
	check := func(name string, r *probeResult) {
		t.Helper()
		if r == nil {
			t.Errorf("%s did not run: %v", name, result.Errors)
			return
		}
		if r.File != "pipe-and-file-ok" {
			t.Errorf("%s file read: %s", name, r.File)
		}
		for _, protocol := range []string{"tcp4", "tcp6", "loopback"} {
			if protocol == "tcp6" && ipv6Err != nil {
				if r.Errors[protocol] == "<nil>" {
					t.Errorf("%s IPv6 connection escaped sandbox", name)
				}
				continue
			}
			if !r.Denied[protocol] {
				t.Errorf("%s %s not denied by OS access control: %s", name, protocol, r.Errors[protocol])
			}
		}
		if !r.Denied["udp"] && r.Errors["udp"] != "<nil>" {
			t.Errorf("%s UDP probe failed before verifying delivery: %s", name, r.Errors["udp"])
		}
	}
	check("plugin", &result)
	check("descendant", result.Child)
	_ = udp.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, _, err := udp.ReadFrom(packet[:]); err == nil {
		t.Fatalf("forbidden UDP packet escaped sandbox: %q", packet[:n])
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatal("UDP observer failed", err)
	}
	if p.AuditError == nil {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			eventsMu.Lock()
			count := len(events)
			eventsMu.Unlock()
			if count > 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		eventsMu.Lock()
		for _, event := range events {
			raw, _ := json.Marshal(event)
			t.Logf("actual Windows denial: %s", raw)
		}
		if len(events) == 0 {
			t.Error("WFP subscribed but no real denial received")
		}
		eventsMu.Unlock()
	}
	if err := p.Close(); err != nil {
		t.Errorf("cleanup: %v", err)
	}
}

func TestWFPWin64Layout(t *testing.T) {
	if unsafe.Sizeof(netHeader{}) != 104 || unsafe.Offsetof(netHeader{}.PackageSID) != 96 || unsafe.Sizeof(netEvent{}) != 120 {
		t.Fatal("WFP native ABI mismatch")
	}
}

func TestConfigPermissionRequiresLocalAbsolutePath(t *testing.T) {
	p := &Process{configRead: []string{"settings.root"}}
	for _, raw := range []string{`{}`, `{"settings":{"root":"relative"}}`, `{"settings":{"root":"\\\\server\\share"}}`} {
		if err := p.PrepareConfig([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid path: %s", raw)
		}
	}
}

// Verify that pipe traffic is binary-safe independently of gRPC framing.
func TestPipeBytes(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data := []byte(strings.Repeat("\x00\xff\n", 32768))
	go func() { _, _ = writer.Write(data); _ = writer.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("pipe altered payload", err)
	}
}
