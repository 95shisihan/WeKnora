//go:build windows

// This probe deliberately attempts forbidden connections. Its stdout reports
// Winsock results; only independently observed WFP events count as audit proof.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func main() {
	result := make(map[string]string)
	for _, address := range []string{"1.1.1.1:443", "[2606:4700:4700::1111]:443"} {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if conn != nil {
			_ = conn.Close()
		}
		if errors.Is(err, syscall.Errno(10013)) {
			result[address] = "WSAEACCES"
		} else {
			result[address] = fmt.Sprint(err)
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
}
