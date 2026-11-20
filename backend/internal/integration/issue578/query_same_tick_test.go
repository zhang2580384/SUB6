//go:build integration

package issue578_test

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestIssue578SameTickQueryPreservesResponse(t *testing.T) {
	dsn := os.Getenv("ISSUE578_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires the isolated issue-578 PostgreSQL database")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	ctx := t.Context()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts (id bigint PRIMARY KEY, extra jsonb, deleted_at timestamptz, updated_at timestamptz); INSERT INTO accounts(id, extra) VALUES (1, '{}'), (2, '{}')`)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	writer := repository.NewAccountRepository(client, db, nil).(service.CodexUsageSnapshotRepository)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	save := func(ts time.Time, source service.CodexObservationSource, fields map[string]any, expected bool) {
		t.Helper()
		fields["codex_usage_updated_at"] = ts.Format(time.RFC3339Nano)
		applied, err := writer.UpdateCodexUsageSnapshot(ctx, 1, fields, source)
		require.NoError(t, err)
		require.Equal(t, expected, applied)
	}
	save(now, service.CodexResponseObservation, map[string]any{"codex_5h_used_percent": 100}, true)
	save(now, service.CodexQueryObservation, map[string]any{"codex_5h_used_percent": 89, "codex_7d_used_percent": 20}, false)
	var five, seven int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (extra->>'codex_5h_used_percent')::int, (extra->>'codex_7d_used_percent')::int FROM accounts WHERE id=1`).Scan(&five, &seven))
	require.Equal(t, 100, five)
	require.Equal(t, 20, seven)
	save(now, service.CodexQueryObservation, map[string]any{"codex_5h_used_percent": 100}, true)
	save(now.Add(time.Nanosecond), service.CodexQueryObservation, map[string]any{"codex_5h_used_percent": 89}, true)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (extra->>'codex_5h_used_percent')::int FROM accounts WHERE id=1`).Scan(&five))
	require.Equal(t, 89, five)
	var empty bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT extra='{}'::jsonb FROM accounts WHERE id=2`).Scan(&empty))
	require.True(t, empty)
}
