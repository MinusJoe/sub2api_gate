package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSettingsSidebarMenuVisibilityRoundTrip(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	ctx := context.Background()
	initial, err := h.settingService.GetAllSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, service.SidebarMenuVisibility{}, initial.SidebarMenuVisibility)

	want := service.SidebarMenuVisibility{Channels: true, Redeem: true, Profile: true}
	rec := doUpdateSettings(t, h, map[string]any{"sidebar_menu_visibility": want}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var response struct {
		Data struct {
			Visibility service.SidebarMenuVisibility `json:"sidebar_menu_visibility"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Equal(t, want, response.Data.Visibility)
	var stored service.SidebarMenuVisibility
	require.NoError(t, json.Unmarshal([]byte(repo.values[service.SettingKeySidebarMenuVisibility]), &stored))
	require.Equal(t, want, stored)

	public, err := h.settingService.GetPublicSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, want, public.SidebarMenuVisibility)
	injected, err := h.settingService.GetPublicSettingsForInjection(ctx)
	require.NoError(t, err)
	payload, ok := injected.(*service.PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.Equal(t, want, payload.SidebarMenuVisibility)

	// An unrelated settings update must preserve the saved menu choices.
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "updated"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	updated, err := h.settingService.GetAllSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, want, updated.SidebarMenuVisibility)

	// Explicit false values close all menus again.
	rec = doUpdateSettings(t, h, map[string]any{"sidebar_menu_visibility": service.SidebarMenuVisibility{}}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	public, err = h.settingService.GetPublicSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, service.SidebarMenuVisibility{}, public.SidebarMenuVisibility)
}

func TestSettingsSidebarMenuVisibilityRejectsInvalidBoolean(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	rec := doUpdateSettings(t, h, map[string]any{
		"sidebar_menu_visibility": map[string]any{"channels": "true"},
	}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.NotContains(t, repo.values, service.SettingKeySidebarMenuVisibility)
}
