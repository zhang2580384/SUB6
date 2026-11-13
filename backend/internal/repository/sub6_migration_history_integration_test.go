//go:build integration

package repository

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// A retired optional feature's ledger entry must survive an upgrade. A numeric
// prefix is not migration identity, and unknown history is not an instruction.
func TestSUB6MigrationKeepsUnknownHistoryAndRepeatedPrefixes(t *testing.T) {
	ctx := context.Background()
	const retired = "333_retired_optional_test_feature.sql"
	const current = "333_current_isolated_history_contract.sql"
	checksum := strings.Repeat("a", 64)
	_, err := integrationDB.ExecContext(ctx, "INSERT INTO schema_migrations(filename,checksum) VALUES($1,$2)", retired, checksum)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM schema_migrations WHERE filename IN ($1,$2)", retired, current)
		require.NoError(t, err)
	})
	f := fstest.MapFS{current: &fstest.MapFile{Data: []byte("SELECT 1;")}}
	for i := 0; i < 2; i++ {
		require.NoError(t, applyMigrationsFS(ctx, integrationDB, f))
		var stored string
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename=$1", retired).Scan(&stored))
		require.Equal(t, checksum, stored)
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE filename IN ($1,$2)", retired, current).Scan(&count))
		require.Equal(t, 2, count)
	}
}
