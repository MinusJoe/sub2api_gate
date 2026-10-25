package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

type wsModeRouterRepo struct {
	openAIFastPolicyRepoStub
	mu                         sync.Mutex
	readErr, writeErr          error
	reads                      atomic.Int32
	blockNext                  atomic.Bool
	readEntered, readRelease   chan struct{}
	writeEntered, writeRelease chan struct{}
}

func (r *wsModeRouterRepo) GetValue(ctx context.Context, key string) (string, error) {
	r.mu.Lock()
	value, exists := r.values[key]
	err := r.readErr
	r.mu.Unlock()
	if key == SettingKeyOpenAIWSModeRouter {
		r.reads.Add(1)
		if r.blockNext.CompareAndSwap(true, false) {
			close(r.readEntered)
			select {
			case <-r.readRelease:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	if err != nil && key == SettingKeyOpenAIWSModeRouter {
		return "", err
	}
	if !exists {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *wsModeRouterRepo) Set(ctx context.Context, key, value string) error {
	if r.writeEntered != nil {
		close(r.writeEntered)
		select {
		case <-r.writeRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writeErr != nil {
		return r.writeErr
	}
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

func newWSModeRouterTestSettings(configured bool, raw *string) (*SettingService, *wsModeRouterRepo, *config.Config) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = configured
	repo := &wsModeRouterRepo{openAIFastPolicyRepoStub: openAIFastPolicyRepoStub{values: map[string]string{}}}
	if raw != nil {
		repo.values[SettingKeyOpenAIWSModeRouter] = *raw
	}
	return NewSettingService(repo, cfg), repo, cfg
}

func TestWSModeRouterInheritanceAndWebOverride(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, raw := range []string{"missing", "null", "true", "false"} {
			t.Run(raw+map[bool]string{false: "/config-off", true: "/config-on"}[configured], func(t *testing.T) {
				var persisted *string
				if raw != "missing" {
					persisted = &raw
				}
				settings, _, cfg := newWSModeRouterTestSettings(configured, persisted)
				gateway := &OpenAIGatewayService{cfg: cfg, settingService: settings}
				view, err := settings.GetOpenAIWSModeRouterSettings(context.Background())
				require.NoError(t, err)
				inherited := raw == "missing" || raw == "null"
				want := raw == "true" || inherited && configured
				require.Equal(t, want, view.Enabled)
				require.Equal(t, configured, view.ConfigEnabled)
				if inherited {
					require.Nil(t, view.Override)
					require.Equal(t, "config", view.Source)
				} else {
					require.NotNil(t, view.Override)
					require.Equal(t, want, *view.Override)
					require.Equal(t, "web", view.Source)
				}
				require.Equal(t, openAIWSModeRouterSnapshot{enabled: want, valid: true}, gateway.openAIWSModeRouterSnapshot(context.Background()))
			})
		}
	}
}

func TestWSModeRouterSaveRestoreAndWriteFailure(t *testing.T) {
	settings, repo, cfg := newWSModeRouterTestSettings(false, nil)
	value := true
	view, err := settings.SetOpenAIWSModeRouterSettings(context.Background(), &value)
	require.NoError(t, err)
	require.True(t, view.Enabled)
	value = false // Neither input nor returned pointers may mutate the cache.
	*view.Override = false
	require.True(t, settings.getOpenAIWSModeRouterCached(context.Background()).enabled)
	require.False(t, cfg.Gateway.OpenAIWS.ModeRouterV2Enabled)
	repo.mu.Lock()
	repo.writeErr = errors.New("write failed")
	repo.mu.Unlock()
	_, err = settings.SetOpenAIWSModeRouterSettings(context.Background(), &value)
	require.Error(t, err)
	require.True(t, settings.getOpenAIWSModeRouterCached(context.Background()).enabled)
	repo.mu.Lock()
	repo.writeErr = nil
	repo.mu.Unlock()
	view, err = settings.SetOpenAIWSModeRouterSettings(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, view.Override)
	require.False(t, view.Enabled)
	require.Equal(t, "config", view.Source)
	repo.mu.Lock()
	require.Equal(t, "null", repo.values[SettingKeyOpenAIWSModeRouter])
	repo.mu.Unlock()
}

func TestWSModeRouterInvalidAndReadFailuresNeverGuessLegacy(t *testing.T) {
	for _, raw := range []string{"", `{}`, `"true"`, `{"enabled":true}`, "broken"} {
		settings, repo, cfg := newWSModeRouterTestSettings(true, &raw)
		gateway := &OpenAIGatewayService{cfg: cfg, settingService: settings}
		_, err := settings.GetOpenAIWSModeRouterSettings(context.Background())
		require.Error(t, err)
		require.False(t, gateway.openAIWSModeRouterSnapshot(context.Background()).valid)
		repo.mu.Lock()
		repo.values[SettingKeyOpenAIWSModeRouter] = "true"
		repo.mu.Unlock()
		settings.storeOpenAIWSModeRouter(openAIWSModeRouterOverride{}, 0)
		require.True(t, gateway.openAIWSModeRouterSnapshot(context.Background()).enabled)
		repo.mu.Lock()
		repo.readErr = errors.New("database unavailable")
		repo.mu.Unlock()
		settings.storeOpenAIWSModeRouter(openAIWSModeRouterOverride{enabled: true, valid: true}, 0)
		require.Equal(t, openAIWSModeRouterSnapshot{enabled: true, valid: true}, gateway.openAIWSModeRouterSnapshot(context.Background()))
	}
	settings, repo, cfg := newWSModeRouterTestSettings(false, nil)
	repo.readErr = errors.New("database unavailable")
	require.False(t, (&OpenAIGatewayService{cfg: cfg, settingService: settings}).openAIWSModeRouterSnapshot(context.Background()).valid)
}

func TestWSModeRouterCacheConcurrentReadsAndSaves(t *testing.T) {
	settings, repo, _ := newWSModeRouterTestSettings(false, nil)
	var workers sync.WaitGroup
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if !settings.getOpenAIWSModeRouterCached(context.Background()).valid {
				t.Error("missing config should be trusted inheritance")
			}
		}()
	}
	workers.Wait()
	require.EqualValues(t, 1, repo.reads.Load())
	repo.mu.Lock()
	repo.values[SettingKeyOpenAIWSModeRouter] = "true"
	repo.mu.Unlock()
	settings.storeOpenAIWSModeRouter(openAIWSModeRouterOverride{}, 0)
	repo.readEntered, repo.readRelease = make(chan struct{}), make(chan struct{})
	repo.blockNext.Store(true)
	fillDone := make(chan struct{})
	go func() { settings.getOpenAIWSModeRouterCached(context.Background()); close(fillDone) }()
	<-repo.readEntered
	saveDone := make(chan error, 1)
	go func() {
		value := false
		_, err := settings.SetOpenAIWSModeRouterSettings(context.Background(), &value)
		saveDone <- err
	}()
	close(repo.readRelease)
	<-fillDone
	require.NoError(t, <-saveDone)
	require.False(t, settings.getOpenAIWSModeRouterCached(context.Background()).enabled)
	for i := range 12 {
		workers.Add(1)
		go func(value bool) {
			defer workers.Done()
			_, err := settings.SetOpenAIWSModeRouterSettings(context.Background(), &value)
			if err != nil {
				t.Error(err)
			}
		}(i%2 == 0)
	}
	workers.Wait()
	repo.mu.Lock()
	persisted := repo.values[SettingKeyOpenAIWSModeRouter]
	repo.mu.Unlock()
	require.Equal(t, persisted == "true", settings.getOpenAIWSModeRouterCached(context.Background()).enabled)
}

func TestWSModeRouterReadDeadlineAndCancelledCaller(t *testing.T) {
	settings, repo, _ := newWSModeRouterTestSettings(false, nil)
	repo.readEntered, repo.readRelease = make(chan struct{}), make(chan struct{})
	repo.blockNext.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	value := settings.getOpenAIWSModeRouterCached(ctx)
	require.False(t, value.valid)
	require.GreaterOrEqual(t, time.Since(started), 200*time.Millisecond)
	require.Less(t, time.Since(started), time.Second)
}

func TestWSModeRouterSlowSaveCannotBlockGatewayRequests(t *testing.T) {
	for _, hasTrustedValue := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "last-known-good"}[hasTrustedValue], func(t *testing.T) {
			settings, repo, _ := newWSModeRouterTestSettings(false, nil)
			if hasTrustedValue {
				settings.storeOpenAIWSModeRouter(openAIWSModeRouterOverride{enabled: true, valid: true}, 0)
			}
			repo.writeEntered, repo.writeRelease = make(chan struct{}), make(chan struct{})
			writer := make(chan error, 1)
			defer func() { close(repo.writeRelease); require.NoError(t, <-writer) }()
			go func() {
				value := false
				_, err := settings.SetOpenAIWSModeRouterSettings(context.Background(), &value)
				writer <- err
			}()
			<-repo.writeEntered
			result := make(chan openAIWSModeRouterOverride, 1)
			go func() { result <- settings.getOpenAIWSModeRouterCached(context.Background()) }()
			select {
			case value := <-result:
				require.Equal(t, hasTrustedValue, value.valid)
				require.Equal(t, hasTrustedValue, value.enabled)
			case <-time.After(100 * time.Millisecond):
				t.Fatal("gateway request waited behind an administrative save")
			}
		})
	}
}
