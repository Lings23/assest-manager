package servicekit

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresExplicitMigrationsSerializeAndDetectDrift(t *testing.T) {
	url := os.Getenv("IAM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IAM_TEST_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	defer pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	source := fstest.MapFS{"migrations/001.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE ` + pgx.Identifier{schema}.Sanitize() + `.marker (id int PRIMARY KEY); INSERT INTO ` + pgx.Identifier{schema}.Sanitize() + `.marker VALUES(1);`)}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := MigrateSchema(t.Context(), url, schema, source); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := CheckSchema(t.Context(), pool, schema, source); err != nil {
		t.Fatal(err)
	}
	source["migrations/001.sql"].Data = append(source["migrations/001.sql"].Data, []byte("-- drift")...)
	if err := CheckSchema(t.Context(), pool, schema, source); err == nil {
		t.Fatal("runtime accepted drift")
	}
	if err := MigrateSchema(t.Context(), url, schema, source); err == nil {
		t.Fatal("migrator accepted drift")
	}
}
