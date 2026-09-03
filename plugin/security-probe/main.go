package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	connection, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second)
	if err == nil {
		_ = connection.Close()
		fmt.Fprintln(os.Stderr, "SECURITY FAILURE: outbound connection succeeded")
		os.Exit(1)
	}
	fmt.Printf("outbound connection blocked: %v\n", err)
}
