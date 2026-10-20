package migrations

import (
	"embed"
	"io/fs"
)

//go:embed platform/*.sql
var platform embed.FS

func Platform() fs.FS {
	sub, err := fs.Sub(platform, "platform")
	if err != nil {
		panic(err)
	}
	return sub
}
