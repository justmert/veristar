package db

import (
	"context"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// DownAll rolls every migration back, as platform_owner, so tests can prove
// the down sections work.
func DownAll(ctx context.Context, cfg AdminConfig, fsys fs.FS) error {
	return DownTo(ctx, cfg, fsys, 0)
}

// DownTo rolls migrations back until version is the newest applied, as
// platform_owner, the role platformctl migrates with.
func DownTo(ctx context.Context, cfg AdminConfig, fsys fs.FS, version int64) error {
	cc, err := pgx.ParseConfig(cfg.Server.connString(cfg.Admin, "dbtest-down"))
	if err != nil {
		return err
	}
	sqlDB := stdlib.OpenDB(*cc, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "set role "+OwnerRole)
		return err
	}))
	defer func() { _ = sqlDB.Close() }()
	p, err := newProvider(sqlDB, fsys)
	if err != nil {
		return err
	}
	_, err = p.DownTo(ctx, version)
	return err
}
