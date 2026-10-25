package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/MACOS-DO/sub4api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetOpenAIWSModeRouterSettings(c *gin.Context) {
	settings, err := h.settingService.GetOpenAIWSModeRouterSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateOpenAIWSModeRouterSettings(c *gin.Context) {
	badRequest := func() {
		response.BadRequest(c, "expected exactly one enabled field: true, false, or null to inherit deployment configuration")
	}
	if c.Request == nil || c.Request.Body == nil {
		badRequest()
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1025))
	if err != nil || len(body) > 1024 {
		badRequest()
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		badRequest()
		return
	}
	key, err := decoder.Token()
	if err != nil || key != "enabled" {
		badRequest()
		return
	}
	var raw json.RawMessage
	if decoder.Decode(&raw) != nil {
		badRequest()
		return
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		badRequest()
		return
	}
	if _, err := decoder.Token(); err != io.EOF {
		badRequest()
		return
	}
	var enabled *bool
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		value := true
		enabled = &value
	case "false":
		value := false
		enabled = &value
	case "null":
	default:
		badRequest()
		return
	}
	settings, err := h.settingService.SetOpenAIWSModeRouterSettings(c.Request.Context(), enabled)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	slog.Info("OpenAI WS mode router settings updated", "enabled", settings.Enabled, "source", settings.Source)
	response.Success(c, settings)
}
