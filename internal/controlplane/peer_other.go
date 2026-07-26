//go:build !linux

package controlplane

import (
	"net"
)

type platformPeerChecker struct{}

func (platformPeerChecker) Check(*net.UnixConn) error {
	// Socket ownership and 0600 mode are the available checks on this platform.
	return nil
}
