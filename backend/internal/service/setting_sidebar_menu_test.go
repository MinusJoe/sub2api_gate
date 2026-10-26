//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSidebarMenuVisibilityDefaultsOff(t *testing.T) {
	for _, raw := range []string{"", "{}", "null", "[]", "invalid", `{"channels":true,"profile":"yes"}`} {
		t.Run(raw, func(t *testing.T) {
			svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{
				SettingKeySidebarMenuVisibility: raw,
			}}, &config.Config{})
			settings, err := svc.GetPublicSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, SidebarMenuVisibility{}, settings.SidebarMenuVisibility)
		})
	}
}
