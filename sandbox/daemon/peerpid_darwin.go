//go:build darwin

package main

import (
	"fmt"
	"net"
	"syscall"
)

func peerPID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("not a unix connection")
	}
	f, err := uc.File()
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fd := int(f.Fd())
	// SOL_LOCAL = 0, LOCAL_PEERPID = 2 on Darwin.
	return syscall.GetsockoptInt(fd, 0, 2)
}