package servicekit

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ConfigurePool applies one explicit per-process budget to every domain.
// An omitted environment override preserves pool_max_conns in DATABASE_URL.
func ConfigurePool(config *pgxpool.Config, fallback int32) error {
	if _, ok := config.ConnConfig.RuntimeParams["pool_max_conns"]; ok {
		return fmt.Errorf("pool_max_conns must be a pgx pool parameter")
	}
	if os.Getenv("DB_MAX_CONNS") != "" {
		value, err := strconv.ParseInt(os.Getenv("DB_MAX_CONNS"), 10, 32)
		if err != nil || value < 1 || value > 100 {
			return fmt.Errorf("DB_MAX_CONNS must be between 1 and 100")
		}
		config.MaxConns = int32(value)
	} else if !containsPoolLimit(config.ConnString()) {
		config.MaxConns = fallback
	}
	if config.MaxConns < 1 || config.MaxConns > 100 {
		return fmt.Errorf("database pool maximum must be between 1 and 100")
	}
	config.MinConns = 0
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnLifetimeJitter = 3 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	return nil
}
