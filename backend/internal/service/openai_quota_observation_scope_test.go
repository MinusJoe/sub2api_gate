//go:build unit

package service

import (
	"context"
	"testing"

	infraerrors "github.com/MACOS-DO/sub4api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// A pinned credits observation must not bypass current target authorization or
// authorize an out-of-scope credential source after shadow resolution.
func TestBuildCodexQuotaHeadersObservationPreservesScope(t *testing.T) {
	for _, tc := range []struct {
		name        string
		targetGroup int64
		sourceGroup int64
		allowed     bool
	}{
		{"both allowed", 1, 1, true},
		{"target moved out of scope", 2, 1, false},
		{"source out of scope", 1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, GroupIDs: []int64{tc.targetGroup}}
			source := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, GroupIDs: []int64{tc.sourceGroup}}
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{1: target, 2: source}}
			svc := &OpenAIQuotaService{accountRepo: repo}
			ctx := context.WithValue(scopedStatisticsServiceContext("openai.quota.read"), openAICreditsObservationContextKey{}, &OpenAICreditsObservation{Target: target, Source: source})
			headers, _, err := svc.buildCodexQuotaHeaders(ctx, 1, "test-token", "test-workspace", false)
			if tc.allowed {
				require.NoError(t, err)
				require.NotEmpty(t, headers)
			} else {
				require.Equal(t, 404, infraerrors.Code(err))
				require.Nil(t, headers)
			}
		})
	}
}
