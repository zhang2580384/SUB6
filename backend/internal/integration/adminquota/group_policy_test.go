//go:build integration

package adminquota_test

import (
	"database/sql"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupPolicyBulkWritePreservesExistingRowsOnFailure(t *testing.T) {
	dsn := os.Getenv("SUB6_ADMIN_QUOTA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires a disposable admin quota test database")
	}
	ctx := t.Context()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE user_group_rate_multipliers(user_id bigint,group_id bigint,rate_multiplier double precision CHECK(rate_multiplier >= 0),rpm_override int CHECK(rpm_override >= 0),denied_models jsonb,created_at timestamptz,updated_at timestamptz,PRIMARY KEY(user_id,group_id)); INSERT INTO user_group_rate_multipliers(user_id,group_id,rate_multiplier,rpm_override,denied_models) VALUES(1,10,2,7,'["blocked"]')`)
	require.NoError(t, err)
	repo := repository.NewUserGroupRateRepository(db)
	check := func() {
		t.Helper()
		var rate float64
		var rpm int
		var model string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT rate_multiplier,rpm_override,denied_models->>0 FROM user_group_rate_multipliers WHERE user_id=1 AND group_id=10`).Scan(&rate, &rpm, &model))
		require.Equal(t, 2.0, rate)
		require.Equal(t, 7, rpm)
		require.Equal(t, "blocked", model)
	}
	negative := -1
	require.Error(t, repo.SyncGroupRateMultipliers(ctx, 10, []service.GroupRateMultiplierInput{{UserID: 2, RateMultiplier: -1}}))
	check()
	require.Error(t, repo.SyncGroupRPMOverrides(ctx, 10, []service.GroupRPMOverrideInput{{UserID: 2, RPMOverride: &negative}}))
	check()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	opCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.SyncGroupRateMultipliers(opCtx, 10, []service.GroupRateMultiplierInput{{UserID: 1, RateMultiplier: 3}, {UserID: 1, RateMultiplier: 4}}))
	var rate float64
	var rpm int
	var model string
	rows, err := tx.Client().QueryContext(opCtx, `SELECT rate_multiplier,rpm_override,denied_models->>0 FROM user_group_rate_multipliers WHERE user_id=1 AND group_id=10`)
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&rate, &rpm, &model))
	require.NoError(t, rows.Close())
	require.Equal(t, 4.0, rate)
	require.Equal(t, 7, rpm)
	require.Equal(t, "blocked", model)
	require.NoError(t, tx.Rollback())
	check()
}
