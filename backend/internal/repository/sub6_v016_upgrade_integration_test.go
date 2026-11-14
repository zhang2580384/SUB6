//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// The fingerprint pins every byte and filename of the actual stable release,
// rather than assuming that removing the last two numeric prefixes recreates it.
func TestSUB6V016UpgradeFromStableKeepsBillingAndRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	names, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	sort.Strings(names)
	oldFS := fstest.MapFS{}
	delta := fstest.MapFS{}
	fingerprint := sha256.New()
	for _, name := range names {
		data, err := fs.ReadFile(migrations.FS, name)
		require.NoError(t, err)
		if name == "343_usage_log_platform_snapshot.sql" || name == usageLogsPlatformIndexMigration {
			delta[name] = &fstest.MapFile{Data: data}
			continue
		}
		oldFS[name] = &fstest.MapFile{Data: data}
		_, _ = fingerprint.Write([]byte("backend/migrations/" + name + "\x00"))
		_, _ = fingerprint.Write(data)
		_, _ = fingerprint.Write([]byte{0})
	}
	require.Len(t, delta, 2)
	require.Equal(t, "a6990935dd92d8132f2d487ba2b3320ac10d8819e72363db2d240bca23e4063f", fmt.Sprintf("%x", fingerprint.Sum(nil)))

	name := fmt.Sprintf("sub6_v016_upgrade_%d", time.Now().UnixNano())
	_, err = integrationDB.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name)+" TEMPLATE template0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DROP DATABASE "+pq.QuoteIdentifier(name)+" WITH (FORCE)")
		require.NoError(t, err)
	})
	dsn := integrationPostgresDSN
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		u.Path = "/" + name
		dsn = u.String()
	} else {
		dsn += " dbname=" + name
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, applyMigrationsFS(ctx, db, oldFS))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	user := mustCreateUser(t, client, &service.User{Email: "upgrade-fixture@example.invalid", Balance: 100})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "synthetic-upgrade-key"})
	account := mustCreateAccount(t, client, &service.Account{Name: "Upgrade fixture", Platform: "openai"})
	billing := NewUsageBillingRepository(client, db)
	command := &service.UsageBillingCommand{UserID: user.ID, APIKeyID: key.ID, RequestID: "upgrade-before", BalanceCost: 1.25}
	first, err := billing.Apply(ctx, command)
	require.NoError(t, err)
	require.True(t, first.Applied)
	legacyInsert := func(request string) {
		_, err := db.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,input_tokens,output_tokens,total_cost,actual_cost)
			VALUES($1,$2,$3,$4,'fixture-model',10,20,2,1)`, user.ID, key.ID, account.ID, request)
		require.NoError(t, err)
	}
	legacyInsert("legacy-before")
	_, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(filename,checksum) VALUES('retired_optional_fixture.sql',$1)`, strings.Repeat("b", 64))
	require.NoError(t, err)

	// A long reader blocks ADD COLUMN. A bounded migration attempt must fail
	// without charging again, dropping history or marking the migration applied.
	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = blocker.ExecContext(ctx, "LOCK TABLE usage_logs IN ACCESS SHARE MODE")
	require.NoError(t, err)
	lockedCtx, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	err = applyMigrationsFS(lockedCtx, db, delta)
	stop()
	require.Error(t, err)
	require.Contains(t, err.Error(), "343_usage_log_platform_snapshot")
	require.NoError(t, blocker.Rollback())
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE filename='343_usage_log_platform_snapshot.sql'`).Scan(&count))
	require.Zero(t, count)

	started := time.Now()
	require.NoError(t, ApplyMigrations(ctx, db))
	t.Logf("isolated fixture migration completed in %s; not a production-size timing estimate", time.Since(started))
	var nullable, dataType string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT is_nullable,data_type FROM information_schema.columns WHERE table_name='usage_logs' AND column_name='platform'`).Scan(&nullable, &dataType))
	require.Equal(t, "YES", nullable)
	require.Equal(t, "character varying", dataType)
	var indexValid bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid='idx_usage_logs_platform_created'::regclass`).Scan(&indexValid))
	require.True(t, indexValid)
	var platform sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT platform FROM usage_logs WHERE request_id='legacy-before'`).Scan(&platform))
	require.False(t, platform.Valid, "never infer historical provider identity")

	// The old column-list writer remains compatible with the additive schema;
	// the new repository records its snapshot beside those legacy rows.
	legacyInsert("legacy-after")
	repo := newUsageLogRepositoryWithSQL(client, db)
	_, err = repo.Create(ctx, &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: "new-after", Model: "fixture-model", Platform: "openai", InputTokens: 10, OutputTokens: 20, TotalCost: 2, ActualCost: 1, CreatedAt: time.Now()})
	require.NoError(t, err)
	rows, page, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, UsageLogFilters{UserID: user.ID, ExactTotal: true})
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.EqualValues(t, 3, page.Total)
	repeated, err := billing.Apply(ctx, command)
	require.NoError(t, err)
	require.False(t, repeated.Applied, "upgrade must preserve the original dedup claim")
	after, err := billing.Apply(ctx, &service.UsageBillingCommand{UserID: user.ID, APIKeyID: key.ID, RequestID: "upgrade-after", BalanceCost: 2.5})
	require.NoError(t, err)
	require.True(t, after.Applied)
	for range 2 {
		require.NoError(t, ApplyMigrations(ctx, db))
		require.NoError(t, applyMigrationsFS(ctx, db, oldFS), "old migration reader must retain newer and retired ledger entries")
	}
	var balance, costs float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.Equal(t, 96.25, balance)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT SUM(actual_cost) FROM usage_logs WHERE user_id=$1", user.ID).Scan(&costs))
	require.Equal(t, 3.0, costs)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=$1", key.ID).Scan(&count))
	require.Equal(t, 2, count)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count))
	require.Equal(t, len(names)+1, count)
}
