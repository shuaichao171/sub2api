package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type codexTicketSettingRepo struct {
	*codexPolicyMigrationRepoStub
	err error
}

type codexTicketProxyRepoStub struct {
	ProxyRepository
	active []Proxy
	all    []Proxy
}

func (r *codexTicketProxyRepoStub) ListActive(context.Context) ([]Proxy, error) {
	return append([]Proxy(nil), r.active...), nil
}

func (r *codexTicketProxyRepoStub) ListByIDs(_ context.Context, ids []int64) ([]Proxy, error) {
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	result := make([]Proxy, 0, len(ids))
	for _, proxy := range r.all {
		if selected[proxy.ID] {
			result = append(result, proxy)
		}
	}
	return result, nil
}

func (r *codexTicketSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.codexPolicyMigrationRepoStub.GetValue(ctx, key)
}

func TestCodexTicketEnabledRuntimeSettingOverridesYaml(t *testing.T) {
	repo := &codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{}}}
	settings := NewSettingService(repo, &config.Config{})
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: false, FailClosed: true}, nil)
	svc.settingService = settings
	account := ticketTestAccount(41)
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID:  41,
		Model:      "gpt-6-astra",
		State:      fakeCodexTicketState(292),
		Length:     292,
		CapturedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	})

	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.False(t, svc.openAICodexTicketEnabled())
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))

	repo.values[SettingKeyOpenAICodexTicketEnabled] = "true"
	settings.InvalidateOpenAICodexTicketEnabledCache()
	require.True(t, svc.openAICodexTicketEnabled())
	h = http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, fakeCodexTicketState(292), h.Get(openAICodexTurnStateHeader))

	repo.values[SettingKeyOpenAICodexTicketEnabled] = "false"
	settings.InvalidateOpenAICodexTicketEnabledCache()
	require.False(t, svc.openAICodexTicketEnabled())
	h = http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))
}

func TestCodexTicketTTLAndReuseRuntimeSettingsOverrideYaml(t *testing.T) {
	repo := &codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{
		SettingKeyOpenAICodexTicketTTLSeconds:   "120",
		SettingKeyOpenAICodexTicketReuseExpired: "true",
	}}}
	settings := NewSettingService(repo, &config.Config{})
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: true, TargetLength: 292, TTLSeconds: 3600, ReuseExpired: false, FailClosed: true,
	}, nil)
	svc.settingService = settings

	cfg := svc.openAICodexTicketConfig()
	require.Equal(t, 120, cfg.TTLSeconds)
	require.True(t, cfg.ReuseExpired)

	account := ticketTestAccount(41)
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: account.ID, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292,
		CapturedAt: time.Now().Add(-10 * time.Minute), ExpiresAt: time.Now().Add(-time.Minute),
	})
	h := http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, fakeCodexTicketState(292), h.Get(openAICodexTurnStateHeader))

	// 运行时可热更新：关闭沿用后过期票据立即失效。
	repo.values[SettingKeyOpenAICodexTicketReuseExpired] = "false"
	settings.InvalidateOpenAICodexTicketReuseExpiredCache()
	require.False(t, svc.openAICodexTicketConfig().ReuseExpired)
	h = http.Header{}
	require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h), ErrOpenAICodexTicketUnavailable)
}

func TestNormalizeOpenAICodexTicketTTLSecondsClamps(t *testing.T) {
	require.Equal(t, openAICodexTicketMinTTLSeconds, normalizeOpenAICodexTicketTTLSeconds(30))
	require.Equal(t, openAICodexTicketDefaultTTLSeconds, normalizeOpenAICodexTicketTTLSeconds(0))
	require.Equal(t, openAICodexTicketMaxTTLSeconds, normalizeOpenAICodexTicketTTLSeconds(openAICodexTicketMaxTTLSeconds+1))
	require.Equal(t, 200, normalizeOpenAICodexTicketTTLSeconds(200))
	require.Equal(t, 0, normalizeOpenAICodexTicketReuseWindowSeconds(0))
	require.Equal(t, openAICodexTicketDefaultReuseWindowSeconds, normalizeOpenAICodexTicketReuseWindowSeconds(-1))
	require.Equal(t, openAICodexTicketMaxTTLSeconds, normalizeOpenAICodexTicketReuseWindowSeconds(openAICodexTicketMaxTTLSeconds+1))
	require.Equal(t, 600, normalizeOpenAICodexTicketReuseWindowSeconds(600))
}

func TestCodexTicketReuseWindowRuntimeSettingOverridesYaml(t *testing.T) {
	repo := &codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{
		SettingKeyOpenAICodexTicketReuseExpired:           "true",
		SettingKeyOpenAICodexTicketReuseExpiredMaxSeconds: "60",
	}}}
	settings := NewSettingService(repo, &config.Config{})
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: true, TargetLength: 292, TTLSeconds: 60, ReuseExpired: true,
		ReuseExpiredMaxSeconds: 3600, FailClosed: true,
	}, nil)
	svc.settingService = settings
	require.Equal(t, 60, svc.openAICodexTicketConfig().ReuseExpiredMaxSeconds)

	account := ticketTestAccount(41)
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: account.ID, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292,
		CapturedAt: time.Now().Add(-10 * time.Minute), ExpiresAt: time.Now().Add(-5 * time.Minute),
	})
	h := http.Header{}
	require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h), ErrOpenAICodexTicketUnavailable)

	// 0 = 不限制，热更新后立即恢复沿用。
	repo.values[SettingKeyOpenAICodexTicketReuseExpiredMaxSeconds] = "0"
	settings.InvalidateOpenAICodexTicketReuseExpiredMaxSecondsCache()
	h = http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, fakeCodexTicketState(292), h.Get(openAICodexTurnStateHeader))
}

func TestRefreshOpenAICodexTickets_DisabledSkipsHarvest(t *testing.T) {
	upstream := &httpUpstreamRecorder{}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:         false,
		HarvestProxyURL: "socks5h://proxy.example.com:1080",
	}, upstream)
	svc.accountRepo = &codexTicketRefreshRepo{accounts: []Account{*ticketTestAccount(41)}}
	svc.refreshOpenAICodexTickets(context.Background())
	require.Empty(t, upstream.requests)
}

func TestCodexTicketProxyMaskAndValidation(t *testing.T) {
	for _, raw := range []string{"http://user:secret@proxy.example.com:8080", "socks5h://user:secret@proxy.example.com:1080", "https://user:secret@[::1]:443"} {
		require.NoError(t, ValidateOpenAICodexTicketHarvestProxyURL(raw))
		masked := MaskProxyURL(raw)
		require.NotContains(t, masked, "secret")
		require.True(t, IsMaskedProxyURL(masked))
	}
	require.True(t, IsMaskedProxyURL(""))
	require.False(t, IsMaskedProxyURL("http://user:secret***suffix@proxy.example.com:8080"))
	for _, raw := range []string{"user:secret@host:1234", "http://user:secret@", "ftp://user:secret@host:1234", "http://user:secret@host:99999", "http://host:1234/?password=secret", "http://host:1234/#secret", "http://user:secret%zz@host:1234"} {
		err := ValidateOpenAICodexTicketHarvestProxyURL(raw)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
		require.Empty(t, MaskProxyURL(raw))
	}
}

func TestCodexTicketSettingsRefreshDoesNotMutateSharedConfig(t *testing.T) {
	cfg := &config.Config{}
	svc := NewSettingService(&codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{SettingKeyOpenAICodexTicketEnabled: "true"}}}, cfg)
	svc.refreshCachedSettings(&SystemSettings{OpenAICodexTicketEnabled: true})
	require.False(t, cfg.Gateway.OpenAICodexTicket.Enabled, "runtime settings must not write the shared immutable startup configuration")
	require.True(t, svc.GetOpenAICodexTicketEnabled(context.Background(), false))
}

func TestCodexTicketProxyPoolAllAndCustomModes(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-time.Minute)
	settingRepo := &codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{}}}
	proxyRepo := &codexTicketProxyRepoStub{
		active: []Proxy{{ID: 1, Name: "first", Status: StatusActive}, {ID: 2, Name: "second", Status: StatusActive}, {ID: 3, Name: "expired", Status: StatusActive, ExpiresAt: &expiredAt}},
		all:    []Proxy{{ID: 1, Name: "first"}, {ID: 2, Name: "second"}, {ID: 3, Name: "expired"}},
	}
	svc := NewSettingService(settingRepo, &config.Config{})
	svc.SetProxyRepository(proxyRepo)

	pool, err := svc.GetCodexTicketPool(context.Background())
	require.NoError(t, err)
	require.Equal(t, CodexTicketPool{Mode: "all", ProxyIDs: []int64{}}, pool)
	available, err := svc.AvailableCodexTicketProxies(context.Background())
	require.NoError(t, err)
	require.Len(t, available, 2)
	require.Equal(t, []int64{1, 2}, []int64{available[0].ID, available[1].ID})

	require.NoError(t, svc.SetCodexTicketPool(context.Background(), CodexTicketPool{Mode: "custom", ProxyIDs: []int64{2}}))
	available, err = svc.AvailableCodexTicketProxies(context.Background())
	require.NoError(t, err)
	require.Len(t, available, 1)
	require.Equal(t, int64(2), available[0].ID)
	proxyRepo.active = []Proxy{{ID: 1, Name: "first", Status: StatusActive}, {ID: 3, Name: "expired", Status: StatusActive, ExpiresAt: &expiredAt}}
	available, err = svc.AvailableCodexTicketProxies(context.Background())
	require.NoError(t, err)
	require.Empty(t, available, "a disabled or deleted custom proxy must stop participating on the next selection")
	proxyRepo.active = []Proxy{{ID: 1, Name: "first", Status: StatusActive}, {ID: 2, Name: "second", Status: StatusActive}, {ID: 3, Name: "expired", Status: StatusActive, ExpiresAt: &expiredAt}}

	proxyRepo.active = append(proxyRepo.active, Proxy{ID: 4, Name: "new", Status: StatusActive})
	proxyRepo.all = append(proxyRepo.all, Proxy{ID: 4, Name: "new"})
	available, err = svc.AvailableCodexTicketProxies(context.Background())
	require.NoError(t, err)
	require.Len(t, available, 1, "custom mode must not include newly added proxies")

	require.NoError(t, svc.SetCodexTicketPool(context.Background(), CodexTicketPool{Mode: "all", ProxyIDs: []int64{2}}))
	available, err = svc.AvailableCodexTicketProxies(context.Background())
	require.NoError(t, err)
	require.Len(t, available, 3)
	require.Equal(t, []int64{1, 2, 4}, []int64{available[0].ID, available[1].ID, available[2].ID})
	require.Error(t, svc.SetCodexTicketPool(context.Background(), CodexTicketPool{Mode: "custom", ProxyIDs: []int64{99}}))
}
