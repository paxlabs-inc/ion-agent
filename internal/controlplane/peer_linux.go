//go:build linux

package controlplane

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

type platformPeerChecker struct{}

func (platformPeerChecker) Check(connection *net.UnixConn) error {
	raw, err := connection.SyscallConn()
	if err != nil {
		return fmt.Errorf("controlplane: inspect local peer: %w", err)
	}
	var credentials *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("controlplane: inspect local peer: %w", err)
	}
	if controlErr != nil {
		return fmt.Errorf("controlplane: read local peer credentials: %w", controlErr)
	}
	if credentials == nil || credentials.Uid != uint32(os.Geteuid()) {
		return ErrUnauthorized
	}
	return nil
}
