//go:build !darwin && !linux

package taskdaemon

import "net"

func authorizeUnixSocketPeer(_ net.Conn) error {
	return nil
}
