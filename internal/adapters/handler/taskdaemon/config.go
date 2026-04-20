package taskdaemon

type Config struct {
	SocketPath     string `env:"TASK_DAEMON_OBSERVER_SOCKET_PATH"`
	HookListenAddr string `env:"TASK_DAEMON_HOOK_LISTEN_ADDRESS" envDefault:"127.0.0.1:4123"`
}
