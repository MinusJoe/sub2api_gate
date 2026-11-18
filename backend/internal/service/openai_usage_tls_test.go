package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardAndRecordUsageKeepEachResponsesTLSObservation(t *testing.T) {
	repo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	upstream := &httpUpstreamRecorder{}
	svc.httpUpstream = upstream
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic"}}
	const body = `{"model":"gpt-5.4","input":"hello","stream":false}`
	for _, status := range []string{tlsfingerprint.StatusApplied, tlsfingerprint.StatusStandard, tlsfingerprint.StatusPlugin, ""} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		upstream.resp = &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"id":"resp_test","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":2}}`)),
		}
		if status != "" {
			tlsfingerprint.SetResponseStatus(upstream.resp, c.Request, status)
		}
		result, err := svc.Forward(context.Background(), c, account, []byte(body))
		require.NoError(t, err)
		require.Equal(t, status, result.TLSFingerprintStatus)
		err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: result, Account: account, APIKey: &APIKey{ID: 1}, User: &User{ID: 1},
		})
		require.NoError(t, err)
		require.NotNil(t, repo.lastLog)
		require.Equal(t, status, repo.lastLog.TLSFingerprintStatus)
	}
}
