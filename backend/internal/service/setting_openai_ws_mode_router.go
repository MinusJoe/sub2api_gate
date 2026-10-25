package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type OpenAIWSModeRouterSettings struct {
	Enabled       bool   `json:"enabled"`
	Override      *bool  `json:"override"`
	Source        string `json:"source"`
	ConfigEnabled bool   `json:"config_enabled"`
}

type openAIWSModeRouterOverride struct {
	enabled   bool
	inherited bool
	valid     bool
}

type cachedOpenAIWSModeRouter struct {
	value     openAIWSModeRouterOverride
	expiresAt time.Time
}

const (
	openAIWSModeRouterCacheTTL     = 5 * time.Second
	openAIWSModeRouterErrorTTL     = time.Second
	openAIWSModeRouterReadTimeout  = 250 * time.Millisecond
	openAIWSModeRouterWriteTimeout = 2 * time.Second
)

func (s *SettingService) readOpenAIWSModeRouter(ctx context.Context) (openAIWSModeRouterOverride, error) {
	if s == nil || s.settingRepo == nil {
		return openAIWSModeRouterOverride{}, errors.New("WS mode router settings are unavailable")
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIWSModeRouter)
	if errors.Is(err, ErrSettingNotFound) {
		return openAIWSModeRouterOverride{inherited: true, valid: true}, nil
	}
	if err != nil {
		return openAIWSModeRouterOverride{}, err
	}
	switch strings.TrimSpace(raw) {
	case "true":
		return openAIWSModeRouterOverride{enabled: true, valid: true}, nil
	case "false":
		return openAIWSModeRouterOverride{valid: true}, nil
	case "null":
		return openAIWSModeRouterOverride{inherited: true, valid: true}, nil
	default:
		return openAIWSModeRouterOverride{}, errors.New("invalid WS mode router setting")
	}
}

func (s *SettingService) openAIWSModeRouterView(value openAIWSModeRouterOverride) *OpenAIWSModeRouterSettings {
	configured := s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled
	view := &OpenAIWSModeRouterSettings{Enabled: configured, ConfigEnabled: configured, Source: "config"}
	if !value.inherited {
		enabled := value.enabled
		view.Enabled, view.Override, view.Source = enabled, &enabled, "web"
	}
	return view
}

func (s *SettingService) GetOpenAIWSModeRouterSettings(ctx context.Context) (*OpenAIWSModeRouterSettings, error) {
	value, err := s.readOpenAIWSModeRouter(ctx)
	if err != nil {
		return nil, err
	}
	return s.openAIWSModeRouterView(value), nil
}

// nil explicitly restores inheritance. Do not mutate the deployment config:
// existing requests/connections retain their immutable routing snapshot.
func (s *SettingService) SetOpenAIWSModeRouterSettings(ctx context.Context, enabled *bool) (*OpenAIWSModeRouterSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("WS mode router settings are unavailable")
	}
	s.openAIWSModeRouterMu.Lock()
	defer s.openAIWSModeRouterMu.Unlock()
	raw := "null"
	value := openAIWSModeRouterOverride{inherited: true, valid: true}
	if enabled != nil {
		value.enabled, value.inherited = *enabled, false
		raw = fmt.Sprint(value.enabled)
	}
	writeCtx, cancel := context.WithTimeout(ctx, openAIWSModeRouterWriteTimeout)
	defer cancel()
	if err := s.settingRepo.Set(writeCtx, SettingKeyOpenAIWSModeRouter, raw); err != nil {
		return nil, err
	}
	s.storeOpenAIWSModeRouter(value, openAIWSModeRouterCacheTTL)
	return s.openAIWSModeRouterView(value), nil
}

func (s *SettingService) storeOpenAIWSModeRouter(value openAIWSModeRouterOverride, ttl time.Duration) {
	s.openAIWSModeRouterCache.Store(&cachedOpenAIWSModeRouter{value: value, expiresAt: time.Now().Add(ttl)})
}

func (s *SettingService) getOpenAIWSModeRouterCached(ctx context.Context) openAIWSModeRouterOverride {
	if s == nil {
		return openAIWSModeRouterOverride{}
	}
	if cached, _ := s.openAIWSModeRouterCache.Load().(*cachedOpenAIWSModeRouter); cached != nil && time.Now().Before(cached.expiresAt) {
		return cached.value
	}
	result, _, _ := s.openAIWSModeRouterSF.Do("mode_router", func() (any, error) {
		if !s.openAIWSModeRouterMu.TryLock() {
			if cached, _ := s.openAIWSModeRouterCache.Load().(*cachedOpenAIWSModeRouter); cached != nil {
				return cached.value, nil
			}
			return openAIWSModeRouterOverride{}, nil
		}
		defer s.openAIWSModeRouterMu.Unlock()
		previous, _ := s.openAIWSModeRouterCache.Load().(*cachedOpenAIWSModeRouter)
		if previous != nil && time.Now().Before(previous.expiresAt) {
			return previous.value, nil
		}
		if ctx == nil {
			ctx = context.Background()
		}
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIWSModeRouterReadTimeout)
		defer cancel()
		value, err := s.readOpenAIWSModeRouter(readCtx)
		ttl := openAIWSModeRouterCacheTTL
		if err != nil {
			// false means legacy, not "WS disabled". Preserve last-known-good;
			// with no trusted value, report unavailable instead of choosing legacy.
			ttl = openAIWSModeRouterErrorTTL
			if previous != nil && previous.value.valid {
				value = previous.value
			}
		}
		s.storeOpenAIWSModeRouter(value, ttl)
		return value, nil
	})
	// A save can publish after the fill unlocks. Prefer its newer snapshot.
	if cached, _ := s.openAIWSModeRouterCache.Load().(*cachedOpenAIWSModeRouter); cached != nil {
		return cached.value
	}
	value, _ := result.(openAIWSModeRouterOverride)
	return value
}

type openAIWSModeRouterContextKey struct{}
type openAIWSModeRouterSnapshot struct{ enabled, valid bool }

func (s *OpenAIGatewayService) openAIWSModeRouterSnapshot(ctx context.Context) openAIWSModeRouterSnapshot {
	if ctx != nil {
		if value, ok := ctx.Value(openAIWSModeRouterContextKey{}).(openAIWSModeRouterSnapshot); ok {
			return value
		}
	}
	configured := s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled
	if s == nil || s.settingService == nil {
		return openAIWSModeRouterSnapshot{enabled: configured, valid: true}
	}
	value := s.settingService.getOpenAIWSModeRouterCached(ctx)
	if !value.valid {
		return openAIWSModeRouterSnapshot{}
	}
	if value.inherited {
		return openAIWSModeRouterSnapshot{enabled: configured, valid: true}
	}
	return openAIWSModeRouterSnapshot{enabled: value.enabled, valid: true}
}

func (s *OpenAIGatewayService) withOpenAIWSModeRouterSnapshot(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(openAIWSModeRouterContextKey{}).(openAIWSModeRouterSnapshot); ok {
		return ctx
	}
	return context.WithValue(ctx, openAIWSModeRouterContextKey{}, s.openAIWSModeRouterSnapshot(ctx))
}

func (s *OpenAIGatewayService) BindOpenAIWSModeRouterSnapshot(c *gin.Context) {
	if c != nil && c.Request != nil {
		c.Request = c.Request.WithContext(s.withOpenAIWSModeRouterSnapshot(c.Request.Context()))
	}
}

func (s *OpenAIGatewayService) openAIWSModeRouterRequestContext(ctx context.Context, c *gin.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if c != nil && c.Request != nil {
		if snapshot, ok := c.Request.Context().Value(openAIWSModeRouterContextKey{}).(openAIWSModeRouterSnapshot); ok {
			return context.WithValue(ctx, openAIWSModeRouterContextKey{}, snapshot)
		}
	}
	ctx = s.withOpenAIWSModeRouterSnapshot(ctx)
	if c != nil && c.Request != nil {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), openAIWSModeRouterContextKey{}, s.openAIWSModeRouterSnapshot(ctx)))
	}
	return ctx
}

func firstOpenAIWSModeRouterContext(contexts []context.Context) context.Context {
	if len(contexts) > 0 && contexts[0] != nil {
		return contexts[0]
	}
	return context.Background()
}
