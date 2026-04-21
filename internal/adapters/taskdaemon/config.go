package taskdaemon

type Config struct {
	SocketPath     string `env:"RIG_DAEMON_SOCKET_PATH"`
	HookListenAddr string `env:"RIG_DAEMON_HOOK_LISTEN_ADDRESS" envDefault:"127.0.0.1:4124"`
	ExecPath       string
	Env            []string
}
