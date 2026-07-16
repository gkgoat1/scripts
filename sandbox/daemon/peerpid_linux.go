//go:build linux

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
	ucred, err := syscall.GetsockoptUcred(fd, syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(ucred.Pid), nil
}