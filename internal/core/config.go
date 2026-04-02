package core

type Config struct {
	BaseBranch     string
	DatabasePath   string
	WorktreeMode   string
	CodexBinary    string
	AttachOnNew    bool
	NonInteractive bool
}
