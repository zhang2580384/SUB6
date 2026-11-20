//go:build integration

package adminquota_test

import (
	"database/sql"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAdminCreationCommitsAccountAndDefaultQuotasTogether(t *testing.T) {
	dsn := os.Getenv("SUB6_ADMIN_QUOTA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires a disposable admin quota test database")
	}
	ctx := t.Context()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var name string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name))
	require.Equal(t, "sub6_admin_quota_test", name, "fixture requires its own database")
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	settings := repository.NewSettingRepository(client)
	quota := repository.NewUserPlatformQuotaServiceAdapter(repository.NewUserPlatformQuotaRepository(client))
	admin := service.NewAdminService(nil, repository.NewUserRepository(client, db), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, client, service.NewSettingService(settings, &config.Config{}), nil, nil, nil, nil, nil, nil, nil, nil, quota)
	email := func() string { return uuid.NewString() + "@example.test" }
	create := func(address string) (*service.User, error) {
		return admin.CreateUser(ctx, &service.CreateUserInput{Email: address, Password: "fixture-password", Concurrency: 1})
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	// The same email must be usable after a failed attempt, including its auth identity.
	address := email()
	require.NoError(t, settings.Set(ctx, service.SettingKeyDefaultPlatformQuotas, `{"openai":{"daily":777},"grok":{"monthly":0}}`))
	_, err = db.ExecContext(ctx, `ALTER TABLE user_platform_quotas ADD CONSTRAINT fixture_quota_failure CHECK (daily_limit_usd IS DISTINCT FROM 777) NOT VALID`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS fixture_quota_failure`)
	})
	user, err := create(address)
	require.ErrorContains(t, err, "fixture_quota_failure")
	require.Nil(t, user)
	require.Zero(t, count(`SELECT count(*) FROM users WHERE email=$1`, address))
	require.Zero(t, count(`SELECT count(*) FROM auth_identities WHERE provider_subject=$1`, address))
	_, err = db.ExecContext(ctx, `ALTER TABLE user_platform_quotas DROP CONSTRAINT fixture_quota_failure`)
	require.NoError(t, err)
	user, err = create(address)
	require.NoError(t, err)
	require.Equal(t, 1, count(`SELECT count(*) FROM auth_identities WHERE user_id=$1`, user.ID))
	require.Equal(t, 1, count(`SELECT count(*) FROM user_platform_quotas WHERE user_id=$1 AND platform='openai' AND daily_limit_usd=777`, user.ID))
	require.Equal(t, 1, count(`SELECT count(*) FROM user_platform_quotas WHERE user_id=$1 AND platform='grok' AND monthly_limit_usd=0 AND daily_limit_usd IS NULL`, user.ID))
	_, err = db.ExecContext(ctx, `DELETE FROM settings WHERE key=$1`, service.SettingKeyDefaultPlatformQuotas)
	require.NoError(t, err)
	user, err = create(email())
	require.NoError(t, err)
	require.Zero(t, count(`SELECT count(*) FROM user_platform_quotas WHERE user_id=$1`, user.ID))
	address = email()
	require.NoError(t, settings.Set(ctx, service.SettingKeyDefaultPlatformQuotas, `{"openai":`))
	user, err = create(address)
	require.Error(t, err)
	require.Nil(t, user)
	require.Zero(t, count(`SELECT count(*) FROM users WHERE email=$1`, address))
}
