package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/handler"
	adminhandler "github.com/MACOS-DO/sub4api/internal/handler/admin"
	servermiddleware "github.com/MACOS-DO/sub4api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWSModeRouterSettingsRequireAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Setting: &adminhandler.SettingHandler{}}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		servermiddleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
	})
	audit := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, audit, stepUp, nil, nil)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		for _, auth := range []string{"", "Bearer user-token"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/api/v1/admin/settings/openai-ws-mode-router", strings.NewReader(`{"enabled":true}`))
			req.Header.Set("Authorization", auth)
			router.ServeHTTP(rec, req)
			if auth == "" {
				require.Equal(t, http.StatusUnauthorized, rec.Code)
			} else {
				require.Equal(t, http.StatusForbidden, rec.Code)
			}
		}
	}
}
