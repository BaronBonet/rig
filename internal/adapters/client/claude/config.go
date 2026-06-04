package claude

type Config struct {
	Binary string `env:"RIG_CLAUDE_BINARY" envDefault:"claude"`
}

type HookForwardingConfig struct {
	CollectorURL string
	HookSecret   string
}
