package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/MACOS-DO/sub4api/ent"
	"github.com/MACOS-DO/sub4api/internal/service"
	"github.com/stretchr/testify/require"
)

type creditRevisionPayload string

func (want creditRevisionPayload) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	var extra map[string]any
	return json.Unmarshal([]byte(raw), &extra) == nil && extra[service.OpenAICreditsRevisionExtraKey] == string(want)
}

func TestOpenAICreditsCASRepository(t *testing.T) {
	for _, tc := range []struct {
		name                string
		revision, workspace string
		affected            int64
		conflict            bool
	}{
		{"advance exact decimal", "9007199254740993", "workspace-a", 1, false},
		{"same identity ABA revision changed", "9007199254740994", "workspace-a", 0, true},
		{"identity switched", "9007199254740993", "workspace-b", 0, true},
		{"atomic update CAS miss", "9007199254740993", "workspace-a", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectBegin()
			tx, err := client.Tx(context.Background())
			require.NoError(t, err)
			mock.ExpectQuery(`SELECT platform, type, credentials, extra, parent_account_id, quota_dimension FROM accounts.*FOR NO KEY UPDATE`).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "credentials", "extra", "parent_account_id", "quota_dimension"}).AddRow("openai", "oauth", `{"chatgpt_account_id":"`+tc.workspace+`","chatgpt_user_id":"user-a"}`, `{"codex_credits_revision":"`+tc.revision+`"}`, nil, "global"))
			if tc.workspace == "workspace-a" && tc.revision == "9007199254740993" {
				mock.ExpectExec(`UPDATE accounts SET extra = .*codex_credits_revision.*`).WithArgs(creditRevisionPayload("9007199254740994"), int64(41), "9007199254740993").WillReturnResult(sqlmock.NewResult(0, tc.affected))
			}
			expected := &service.Account{ID: 41, Platform: "openai", Type: "oauth", Credentials: map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_user_id": "user-a"}, Extra: map[string]any{service.OpenAICreditsRevisionExtraKey: "9007199254740993"}}
			repo := newAccountRepositoryWithSQL(client, nil, nil)
			err = repo.UpdateOpenAICreditsSnapshot(dbent.NewTxContext(context.Background(), tx), service.OpenAICreditsObservation{Target: expected, Source: expected}, map[string]any{"codex_credits_snapshot": map[string]any{"credits": nil, "fetched_at": 123}})
			if tc.conflict {
				require.ErrorIs(t, err, service.ErrOpenAICreditsObservationConflict)
			} else {
				require.NoError(t, err)
			}
			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLockAndMergeAccountExtraPreservesLatestCredits(t *testing.T) {
	for _, tc := range []struct {
		name, current   string
		present         bool
		identityChanged bool
	}{
		{"latest exhausted balance beats stale positive balance", `{"codex_credits_snapshot":{"credits":{"has_credits":false},"fetched_at":200}}`, true, false},
		{"reject injected balance", `{}`, false, false},
		{"invalid stored extra rejects injection", `[]`, false, false},
		{"identity change invalidates old positive balance", `{"codex_credits_snapshot":{"credits":{"has_credits":true},"fetched_at":200}}`, false, true},
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
					AddRow(!tc.identityChanged, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, nil, nil, []byte(tc.current)))
			account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "test"}, Extra: map[string]any{
				"codex_credits_snapshot": map[string]any{"credits": map[string]any{"has_credits": true}, "fetched_at": 100}, service.OpenAICreditOverageExtraKey: true,
			}}
			extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
			require.NoError(t, err)
			require.Equal(t, true, extra[service.OpenAICreditOverageExtraKey])
			if tc.present {
				snapshot, ok := extra["codex_credits_snapshot"].(map[string]any)
				require.True(t, ok)
				require.EqualValues(t, 200, snapshot["fetched_at"])
				credits, ok := snapshot["credits"].(map[string]any)
				require.True(t, ok)
				require.Equal(t, false, credits["has_credits"])
			} else {
				require.NotContains(t, extra, "codex_credits_snapshot")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
