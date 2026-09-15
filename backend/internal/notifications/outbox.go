// Package notifications delivers committed room events independently of gameplay.
package notifications

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"sync"
	"time"
)

type Sender func(context.Context, int64, string) error

func Run(ctx context.Context, pool *pgxpool.Pool, send Sender) {
	// Slow Telegram requests must not consume the gameplay connection pool.
	cfg := pool.Config()
	cfg.MaxConns, cfg.MinConns = 4, 0
	deliveryPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Error("telegram delivery pool initialization failed")
		return
	}
	defer deliveryPool.Close()
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				work, err := DeliverOne(ctx, deliveryPool, send)
				if err != nil && ctx.Err() == nil {
					slog.Warn("telegram event delivery failed; will retry")
				}
				if !work || err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
				}
			}
		}()
	}
	workers.Wait()
}

// A transaction locks only the delivery row, never a room. Earlier messages for
// the same recipient must complete first, including across multiple workers.
func DeliverOne(ctx context.Context, pool *pgxpool.Pool, send Sender) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var id, user int64
	var body string
	err = tx.QueryRow(ctx, `SELECT o.id,o.user_id,o.body FROM telegram_outbox o
 WHERE o.next_attempt <= now()
 AND NOT EXISTS (SELECT 1 FROM telegram_outbox earlier WHERE earlier.user_id=o.user_id AND earlier.id<o.id)
 ORDER BY o.id FOR UPDATE OF o SKIP LOCKED LIMIT 1`).Scan(&id, &user, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var subscribed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bot_sessions s
 JOIN room_members m ON m.room_id=s.room_id AND m.user_id=s.user_id
 JOIN telegram_outbox o ON o.room_id=s.room_id AND o.user_id=s.user_id
 WHERE o.id=$1 AND s.notifications)`, id).Scan(&subscribed)
	if err != nil {
		return true, err
	}
	var sendErr error
	if subscribed {
		sendCtx, stop := context.WithTimeout(ctx, 20*time.Second)
		sendErr = send(sendCtx, user, body)
		stop()
	}
	if sendErr == nil {
		_, err = tx.Exec(ctx, "DELETE FROM telegram_outbox WHERE id=$1", id)
	} else {
		var permanent interface{ Permanent() bool }
		if errors.As(sendErr, &permanent) && permanent.Permanent() {
			if _, err = tx.Exec(ctx, "UPDATE bot_sessions SET notifications=false WHERE user_id=$1", user); err != nil {
				return true, err
			}
			_, err = tx.Exec(ctx, "DELETE FROM telegram_outbox WHERE user_id=$1", user)
		} else {
			_, err = tx.Exec(ctx, `UPDATE telegram_outbox SET attempts=attempts+1,
 next_attempt=now()+make_interval(secs => LEAST(300,5*(attempts+1))) WHERE id=$1`, id)
		}
	}
	if err != nil {
		return true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, err
	}
	return true, sendErr
}
