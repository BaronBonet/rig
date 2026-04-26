//go:build darwin

package taskdaemon

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func authorizeUnixSocketPeer(conn net.Conn) error {
	peerUID, err := unixSocketPeerUID(conn)
	if err != nil {
		return err
	}

	return authorizeUnixSocketPeerUID(peerUID, uint32(os.Geteuid()))
}

func unixSocketPeerUID(conn net.Conn) (uint32, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("task daemon connection is %T, not *net.UnixConn", conn)
	}

	rawConn, err := unixConn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("access task daemon connection fd: %w", err)
	}

	var (
		peerUID uint32
		peerErr error
	)
	if err := rawConn.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			peerErr = err
			return
		}
		peerUID = cred.Uid
	}); err != nil {
		return 0, fmt.Errorf("inspect task daemon peer credentials: %w", err)
	}
	if peerErr != nil {
		return 0, fmt.Errorf("inspect task daemon peer credentials: %w", peerErr)
	}

	return peerUID, nil
}
