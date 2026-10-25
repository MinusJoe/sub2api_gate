package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/MACOS-DO/sub4api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type wsModeRouterHandlerRepo struct{ fastBridgeHandlerRepo }

func (r *wsModeRouterHandlerRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, exists := r.values[key]; exists {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func wsModeRouterHandlerRequest(handler *SettingHandler, method, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/api/v1/admin/settings/openai-ws-mode-router", strings.NewReader(body))
	if method == http.MethodGet {
		handler.GetOpenAIWSModeRouterSettings(c)
	} else {
		handler.UpdateOpenAIWSModeRouterSettings(c)
	}
	return rec
}

func TestSettingHandlerWSModeRouterStrictValuesAndInheritance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &wsModeRouterHandlerRepo{fastBridgeHandlerRepo: fastBridgeHandlerRepo{settingHandlerRepoStub: settingHandlerRepoStub{values: map[string]string{}}}}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	handler := NewSettingHandler(service.NewSettingService(repo, cfg), nil, nil, nil, nil, nil, nil)
	for _, body := range []string{`{}`, `{"enabled":"true"}`, `{"enabled":1}`, `{"enabled":true,"enabled":false}`, `{"enabled":true,"extra":1}`, `{"enabled":true}{"enabled":false}`, `null`, strings.Repeat(" ", 1025)} {
		rec := wsModeRouterHandlerRequest(handler, http.MethodPut, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Empty(t, repo.values)
	}
	for _, body := range []string{`{"enabled":false}`, `{"enabled":true}`, `{"enabled":null}`} {
		rec := wsModeRouterHandlerRequest(handler, http.MethodPut, body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var result struct {
			Data service.OpenAIWSModeRouterSettings `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		require.True(t, result.Data.ConfigEnabled)
		if strings.Contains(body, "null") {
			require.Nil(t, result.Data.Override)
			require.True(t, result.Data.Enabled)
			require.Equal(t, "config", result.Data.Source)
		} else {
			require.NotNil(t, result.Data.Override)
			require.Equal(t, "web", result.Data.Source)
		}
		require.True(t, cfg.Gateway.OpenAIWS.ModeRouterV2Enabled)
	}
	rec := wsModeRouterHandlerRequest(handler, http.MethodGet, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"override":null`)
}
