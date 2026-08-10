// Package db 는 서버가 쓰는 Postgres 커넥션 풀을 만든다.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open 은 커넥션 풀을 만들고 실제로 연결되는지 확인한다.
// 풀은 lazy 라서 Ping 을 하지 않으면 첫 요청에 가서야 연결 실패를 알게 된다.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL 파싱: %w", err)
	}

	cfg.MaxConns = 10
	cfg.MinConns = 2
	// 오래 붙잡고 있던 커넥션은 주기적으로 버린다. 프록시나 DB 재시작 뒤에
	// 죽은 커넥션을 계속 쥐고 있는 상황을 피한다.
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("커넥션 풀 생성: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("DB 연결 실패 (docker compose up -d 확인): %w", err)
	}

	return pool, nil
}
