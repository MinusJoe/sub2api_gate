package repository

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/MACOS-DO/sub4api/ent"
	"github.com/MACOS-DO/sub4api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLockAndMergeAccountExtraPreservesLatestCredits(t *testing.T) {
	for _, tc := range []struct {
		name, current string
		present       bool
	}{
		{"latest exhausted balance beats stale positive balance", `{"codex_credits_snapshot":{"credits":{"has_credits":false},"fetched_at":200}}`, true},
		{"reject injected balance", `{}`, false},
		{"invalid stored extra rejects injection", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectQuery(`(?s)SELECT.*FOR NO KEY UPDATE`).
				WithArgs(int64(41), service.PlatformOpenAI, service.AccountTypeOAuth, `{"access_token":"test"}`, nil).
				WillReturnRows(sqlmock.NewRows([]string{"identity_unchanged", "ollama_group_unchanged", "ollama_proxy_unchanged", "enabled", "rate_sync_enabled", "snapshot", "ollama_session", "ollama_auto", "ollama_snapshot", "opencode_group_unchanged", "opencode_auto", "opencode_snapshot", "grok_observed_models", "upstream_model_metadata", "current_extra"}).
					AddRow(true, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, nil, nil, []byte(tc.current)))
			account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "test"}, Extra: map[string]any{
				"codex_credits_snapshot": map[string]any{"credits": map[string]any{"has_credits": true}, "fetched_at": 100}, service.OpenAICreditOverageExtraKey: true,
			}}
			extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
			require.NoError(t, err)
			require.Equal(t, true, extra[service.OpenAICreditOverageExtraKey])
			if tc.present {
				snapshot := extra["codex_credits_snapshot"].(map[string]any)
				require.EqualValues(t, 200, snapshot["fetched_at"])
				require.Equal(t, false, snapshot["credits"].(map[string]any)["has_credits"])
			} else {
				require.NotContains(t, extra, "codex_credits_snapshot")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
