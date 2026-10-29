// Package db, şema göçlerini binary'ye gömer — konteyner dış araç istemez;
// `core migrate` alt komutu ve MIGRATE_ON_START bu FS'ten okur.
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
