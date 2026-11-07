package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/MACOS-DO/sub4api/ent"
	"github.com/MACOS-DO/sub4api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateExtraCodexDisplaySnapshotsAvoidSchedulerOutbox(t *testing.T) {
	for _, key := range []string{"codex_credits_snapshot", "codex_referral_snapshot"} {
		for _, tc := range []struct {
			name             string
			value            any
			schedulingChange bool
		}{
			{"refresh", map[string]any{"fetched_at": 1770000000}, false},
			{"invalidate", nil, false},
			{"mixed scheduling update", nil, true},
		} {
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
				t.Cleanup(func() { _ = client.Close() })
				repo := newAccountRepositoryWithSQL(client, db, nil)
				updates := map[string]any{key: tc.value}
				if tc.schedulingChange {
					updates["openai_compact_enabled"] = true
					mock.ExpectBegin()
				}
				// Credits affect scheduling and are managed by the revision-checked
				// UpdateOpenAICreditsSnapshot path. Generic extra writes must not
				// refresh or invalidate that snapshot, even in mixed updates.
				persisted := map[string]any{}
				if key != "codex_credits_snapshot" {
					persisted[key] = tc.value
				}
				if tc.schedulingChange {
					persisted["openai_compact_enabled"] = true
				}
				payload, err := json.Marshal(persisted)
				require.NoError(t, err)
				if len(persisted) > 0 {
					mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) || $1::jsonb, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL")).
						WithArgs(string(payload), int64(27)).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				if tc.schedulingChange {
					mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
						WithArgs(service.SchedulerOutboxEventAccountChanged, int64(27), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
					mock.ExpectCommit()
				}
				// Display-only changes avoid a transaction/outbox event; managed
				// credits-only writes must not issue any SQL through UpdateExtra.
				require.NoError(t, repo.UpdateExtra(context.Background(), 27, updates))
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
