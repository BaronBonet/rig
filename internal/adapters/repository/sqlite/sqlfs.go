package sqlite

import "embed"

//go:embed bootstrap/*.sql migrations/*.sql
var sqlFiles embed.FS
