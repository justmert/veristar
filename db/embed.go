// Package db carries the platform database migrations into the binaries that
// apply them, so a deployed platformctl never reads migrations from disk.
package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var files embed.FS

// Migrations returns the goose migration files at the root of a file system.
func Migrations() fs.FS {
	sub, err := fs.Sub(files, "migrations")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this path is a constant
		panic(err)
	}
	return sub
}
