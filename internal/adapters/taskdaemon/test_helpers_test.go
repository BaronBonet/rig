package taskdaemon

import (
	"context"
	"errors"
	"net"
)

func listenUnixSocket(ctx context.Context, socketPath string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(ctx, "unix", socketPath)
}

func isTimeoutNetError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
