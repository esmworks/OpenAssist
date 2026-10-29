// Package store — PostgreSQL erişim katmanı.
//
// Tip güvenliği zinciri (docs/design/2026-08-13-faz0-tip-guvenligi.md):
//   - şema:    db/migrations (goose; embed.FS ile binary'de)
//   - sorgu:   db/queries/*.sql → sqlc → internal/store/db (üretilmiş koda elle dokunulmaz)
//   - runtime: pgx/v5 bağlantı havuzu
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // goose için database/sql sürücüsü ("pgx")
	"github.com/pressly/goose/v3"

	dbfs "github.com/esmworks/OpenAssist/db"
	sqlcdb "github.com/esmworks/OpenAssist/internal/store/db"
)

type Store struct {
	pool *pgxpool.Pool
	// Q — sqlc üretimi tipli sorgular; alan erişimi bilinçli olarak dışa açık.
	Q *sqlcdb.Queries
}

func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("store: havuz kurulamadı: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: veritabanına ulaşılamadı: %w", err)
	}
	return &Store{pool: pool, Q: sqlcdb.New(pool)}, nil
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Pool, LISTEN/NOTIFY gibi ham bağlantı isteyen katmanlara havuzu açar.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) Close() { s.pool.Close() }

// Migrate, gömülü göçleri sırayla uygular. Tekrar çağrılması güvenlidir
// (goose sürüm tablosuyla idempotent).
func (s *Store) Migrate(ctx context.Context) error {
	// goose database/sql ister; havuzun bağlantı dizesiyle geçici bağlantı açılır.
	sqldb, err := sql.Open("pgx", s.pool.Config().ConnString())
	if err != nil {
		return fmt.Errorf("store: göç bağlantısı: %w", err)
	}
	defer func() { _ = sqldb.Close() }()

	goose.SetBaseFS(dbfs.Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("store: goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqldb, "migrations"); err != nil {
		return fmt.Errorf("store: göç uygulanamadı: %w", err)
	}
	return nil
}
