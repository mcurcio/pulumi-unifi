// fwd is a static TCP forwarder for the private e2e tier
// (docs/design/private-binding.md §11). Copied into the UniFi OS Server
// container, it exposes the Network application's loopback-only port:
//
//	fwd 0.0.0.0:18081 127.0.0.1:8081
package main

import (
	"fmt"
	"io"
	"net"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: fwd <listen> <target>")
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			t, err := net.Dial("tcp", os.Args[2])
			if err != nil {
				return
			}
			defer t.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(t, c); done <- struct{}{} }()
			go func() { _, _ = io.Copy(c, t); done <- struct{}{} }()
			<-done
		}(c)
	}
}
