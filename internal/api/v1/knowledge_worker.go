package v1

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/core/knowledge"
	"github.com/rs/zerolog/log"
)

// The server owns this loop; Stop cancels in-flight LLM calls and joins it.
func runKnowledge(ctx context.Context, db *pgxpool.Pool, svc *knowledge.Service, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		compileKnowledge(ctx, db, svc)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func compileKnowledge(ctx context.Context, db *pgxpool.Pool, svc *knowledge.Service) {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(743901, 1)").Scan(&locked); err != nil || !locked {
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, "SELECT pg_advisory_unlock(743901, 1)"); err != nil {
			_ = conn.Conn().Close(cleanup)
		}
	}()
	for offset := 0; ctx.Err() == nil; {
		result, err := svc.CompileBatch(ctx, 1, offset)
		if err != nil {
			if ctx.Err() == nil {
				log.Error().Err(err).Msg("WikiLLM compilation failed")
			}
			return
		}
		for _, failure := range result.Errors {
			log.Warn().Str("entity_type", failure.Kind).Str("entity_id", failure.ID).Str("error", failure.Error).Msg("WikiLLM entity compilation failed")
		}
		if result.NextOffset >= result.Total || result.NextOffset <= offset {
			return
		}
		offset = result.NextOffset
	}
}
