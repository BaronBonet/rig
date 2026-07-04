package taskdaemon

import (
	"context"
	"errors"
	"net/http"

	"github.com/BaronBonet/rig/internal/core"
)

// server runs the daemon-side transports: the Unix socket serving
// core.TaskService and the loopback HTTP server exposing provider hook
// routes.
type server struct {
	service        core.TaskService
	socketPath     string
	hookListenAddr string
	stop           func()
	hookRoutes     []core.TaskDaemonHookRoute
}

func (s *server) Serve(ctx context.Context) error {
	httpHookListener, err := listenForHTTPHooks(ctx, s.hookListenAddr)
	if err != nil {
		return err
	}
	defer httpHookListener.Close()

	unixSocketServer := &unixSocketServer{
		socketPath: s.socketPath,
		service:    s.service,
		stop:       s.stop,
	}

	httpHookServer := &http.Server{Handler: newHTTPHookServer(s.hookRoutes)}

	errCh := make(chan error, 2)
	go func() {
		errCh <- unixSocketServer.Serve(ctx)
	}()
	go func() {
		errCh <- httpHookServer.Serve(httpHookListener)
	}()

	select {
	case <-ctx.Done():
	case serveErr := <-errCh:
		if serveErr != nil && !errorsIsHTTPServerClosed(serveErr) {
			_ = httpHookServer.Shutdown(context.Background())
			return serveErr
		}
	}

	_ = httpHookServer.Shutdown(context.Background())
	return nil
}

func errorsIsHTTPServerClosed(err error) bool {
	return errors.Is(err, http.ErrServerClosed)
}
