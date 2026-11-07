package repository

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	dbent "github.com/MACOS-DO/sub4api/ent"
	"github.com/MACOS-DO/sub4api/internal/service"
)

// Lock in ID order so a shadow query and an account edit cannot deadlock by
// acquiring the display and credential rows in opposite orders.
func lockOpenAICreditsAccounts(ctx context.Context, client *dbent.Client, ids []int64) (map[int64]*service.Account, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	result := make(map[int64]*service.Account, len(ids))
	for _, id := range ids {
		rows, err := client.QueryContext(ctx, `SELECT platform, type, credentials, extra, parent_account_id, quota_dimension FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE`, id)
		if err != nil {
			return nil, err
		}
		account := &service.Account{ID: id}
		var credentials, extra []byte
		var parent *int64
		if !rows.Next() {
			err := rows.Err()
			_ = rows.Close()
			if err != nil {
				return nil, err
			}
			return nil, service.ErrAccountNotFound
		}
		err = rows.Scan(&account.Platform, &account.Type, &credentials, &extra, &parent, &account.QuotaDimension)
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(credentials, &account.Credentials) != nil || (len(extra) > 0 && json.Unmarshal(extra, &account.Extra) != nil) {
			return nil, service.ErrOpenAICreditsObservationConflict
		}
		account.ParentAccountID = parent
		result[id] = account
	}
	return result, nil
}

func sameOpenAICreditsObservation(current, expected *service.Account) bool {
	if current == nil || expected == nil || service.OpenAICreditsIdentity(current) != service.OpenAICreditsIdentity(expected) {
		return false
	}
	a, ae := service.OpenAICreditsRevision(current.Extra)
	b, be := service.OpenAICreditsRevision(expected.Extra)
	return ae == nil && be == nil && a == b
}

func (r *accountRepository) UpdateOpenAICreditsSnapshot(ctx context.Context, observation service.OpenAICreditsObservation, updates map[string]any) error {
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		return scoped.UpdateOpenAICreditsSnapshot(ctx, observation, updates)
	}
	if observation.Target == nil || observation.Source == nil {
		return service.ErrOpenAICreditsObservationConflict
	}
	if dbent.TxFromContext(ctx) == nil {
		tx, err := r.client.Tx(ctx)
		if errors.Is(err, dbent.ErrTxStarted) {
			return r.updateOpenAICreditsSnapshotInTx(ctx, observation, updates)
		}
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := r.updateOpenAICreditsSnapshotInTx(dbent.NewTxContext(ctx, tx), observation, updates); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		r.syncSchedulerAccountSnapshot(ctx, observation.Target.ID)
		return nil
	}
	return r.updateOpenAICreditsSnapshotInTx(ctx, observation, updates)
}

func (r *accountRepository) updateOpenAICreditsSnapshotInTx(ctx context.Context, observation service.OpenAICreditsObservation, updates map[string]any) error {
	client := clientFromContext(ctx, r.client)
	accounts, err := lockOpenAICreditsAccounts(ctx, client, []int64{observation.Target.ID, observation.Source.ID})
	if err != nil {
		return err
	}
	target, source := accounts[observation.Target.ID], accounts[observation.Source.ID]
	if !sameOpenAICreditsObservation(target, observation.Target) || !sameOpenAICreditsObservation(source, observation.Source) || source.IsShadow() || source.Platform != service.PlatformOpenAI || source.Type != service.AccountTypeOAuth {
		return service.ErrOpenAICreditsObservationConflict
	}
	if target.ID != source.ID && (target.ParentAccountID == nil || *target.ParentAccountID != source.ID || !target.IsShadow()) {
		return service.ErrOpenAICreditsObservationConflict
	}
	revision, err := service.NextOpenAICreditsRevision(target.Extra)
	if err != nil {
		return err
	}
	updates = maps.Clone(updates)
	if updates == nil {
		return service.ErrOpenAICreditsObservationConflict
	}
	if _, ok := updates["codex_credits_snapshot"]; !ok {
		return service.ErrOpenAICreditsObservationConflict
	}
	updates[service.OpenAICreditsRevisionExtraKey] = revision
	payload, err := json.Marshal(updates)
	if err != nil {
		return err
	}
	expectedRevision, _ := service.OpenAICreditsRevision(target.Extra)
	result, err := client.ExecContext(ctx, `UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) || $1::jsonb, updated_at = NOW() WHERE id = $2 AND COALESCE(extra ->> 'codex_credits_revision', '0') = $3 AND deleted_at IS NULL`, string(payload), target.ID, expectedRevision)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return service.ErrOpenAICreditsObservationConflict
	}
	return nil
}

// SQL mirrors the stable credential portion of service.OpenAICreditsIdentity.
// numeric is exact decimal in PostgreSQL, unlike a float timestamp or Lua number.
func openAICreditsCredentialIdentitySQL(credentials string) string {
	workspace := "COALESCE(NULLIF(btrim(" + credentials + " ->> 'chatgpt_account_id'), ''), NULLIF(btrim(" + credentials + " ->> 'organization_id'), ''), '')"
	user := "COALESCE(btrim(" + credentials + " ->> 'chatgpt_user_id'), '')"
	mode := "lower(btrim(COALESCE(" + credentials + " ->> 'auth_mode', '')))"
	return "jsonb_build_array(" + workspace + ", " + mode + ", CASE WHEN lower(btrim(" + mode + ")) = 'agentidentity' THEN jsonb_build_array(COALESCE(" + credentials + " ->> 'agent_private_key',''), COALESCE(" + credentials + " ->> 'agent_runtime_id','')) WHEN " + workspace + " <> '' AND " + user + " <> '' THEN to_jsonb(" + user + ") ELSE COALESCE(" + credentials + ", '{}'::jsonb) END)"
}

func openAICreditsInvalidationSQL(extra, nextCredentials string) string {
	changed := "platform = 'openai' AND " + openAICreditsCredentialIdentitySQL("credentials") + " IS DISTINCT FROM " + openAICreditsCredentialIdentitySQL(nextCredentials)
	// Revisions are server-owned canonical decimal strings. An invalid existing
	// revision fails the statement instead of resetting it and permitting ABA.
	revision := "((COALESCE(extra ->> 'codex_credits_revision', '0'))::numeric + 1)::text"
	return "CASE WHEN " + changed + " THEN ((" + extra + ") - 'codex_credits_snapshot') || jsonb_build_object('codex_credits_revision', " + revision + ") ELSE " + extra + " END"
}
