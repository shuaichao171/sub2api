package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type codexTicketAttemptMemoryRepo struct {
	inserted  []CodexTicketAttempt
	insertErr error
}

func (r *codexTicketAttemptMemoryRepo) Insert(_ context.Context, attempt *CodexTicketAttempt) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	r.inserted = append(r.inserted, *attempt)
	return nil
}
func (r *codexTicketAttemptMemoryRepo) List(context.Context, int64, string, bool, int, int) ([]CodexTicketAttempt, int64, error) {
	return nil, 0, nil
}
func (r *codexTicketAttemptMemoryRepo) Cleanup(context.Context) error { return nil }
func (r *codexTicketAttemptMemoryRepo) TryLock(context.Context, int64, string) (func(), bool, error) {
	return func() {}, true, nil
}

func (r *codexTicketQuotaRepo) GetByID(context.Context, int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.account
	return &account, nil
}

func TestManualCodexTicketHarvestBypassesRateLimitAndKeepsItReadOnly(t *testing.T) {
	account := ticketTestAccount(41)
	account.Status = StatusActive
	resetAt := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	account.RateLimitResetAt = &resetAt
	repo := &codexTicketQuotaRepo{account: *account}
	history := &codexTicketAttemptMemoryRepo{}
	response := codexTicketResponse()
	response.StatusCode = http.StatusTooManyRequests
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example:8080", Models: []string{"gpt-6-astra"}}, &httpUpstreamRecorder{responses: []*http.Response{response}})
	svc.accountRepo, svc.openaiCodexTicketHistory = repo, history

	result, err := svc.ManualCodexTicketHarvest(context.Background(), account.ID, "gpt-6-astra")
	require.NoError(t, err)
	require.Equal(t, "success", result.Outcome)
	require.Equal(t, "manual", result.Trigger)
	require.True(t, result.HistoryRecorded)
	require.True(t, result.TicketStatus.Ready)
	require.Equal(t, resetAt, *repo.account.RateLimitResetAt)
	require.Len(t, history.inserted, 1)
	require.Equal(t, "manual", history.inserted[0].Trigger)
}

func TestManualCodexTicketHarvestStoresResponseCookies(t *testing.T) {
	account := ticketTestAccount(41)
	account.Status = StatusActive
	repo := &codexTicketQuotaRepo{account: *account}
	response := codexTicketResponse()
	response.Header.Add("Set-Cookie", "__cf_bm=abc; Path=/; Secure")
	response.Header.Add("Set-Cookie", "oai-did=xyz; Path=/")
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: true, HarvestProxyURL: "http://proxy.example:8080",
		Models: []string{"gpt-6-astra"}, TargetLength: 292, TTLSeconds: 60,
	}, &httpUpstreamRecorder{responses: []*http.Response{response}})
	svc.accountRepo = repo

	result, err := svc.ManualCodexTicketHarvest(context.Background(), account.ID, "gpt-6-astra")
	require.NoError(t, err)
	require.Equal(t, "success", result.Outcome)
	stored := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, stored)
	require.Equal(t, "__cf_bm=abc; oai-did=xyz", stored.Cookie)
	require.WithinDuration(t, time.Now().Add(60*time.Second), stored.ExpiresAt, 5*time.Second)
}

func TestManualCodexTicketFailurePreservesExistingTicketAndReportsHistoryFailure(t *testing.T) {
	account := ticketTestAccount(41)
	account.Status = StatusActive
	repo := &codexTicketQuotaRepo{account: *account}
	history := &codexTicketAttemptMemoryRepo{insertErr: errors.New("database unavailable")}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example:8080", Models: []string{"gpt-6-astra"}}, &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}}})
	svc.accountRepo, svc.openaiCodexTicketHistory = repo, history
	existing := &openAICodexTicket{AccountID: account.ID, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	svc.storeOpenAICodexTicket(context.Background(), account, existing)

	result, err := svc.ManualCodexTicketHarvest(context.Background(), account.ID, "gpt-6-astra")
	require.NoError(t, err)
	require.Equal(t, "miss", result.Outcome)
	require.False(t, result.HistoryRecorded)
	require.Equal(t, existing.State, svc.lookupOpenAICodexTicket(account, "gpt-6-astra").State)
}

func scheduledCodexTicketNext(t *testing.T, svc *OpenAIGatewayService, accountID int64, model string) time.Time {
	t.Helper()
	value, ok := svc.openaiCodexTicketNextAttempt.Load(openAICodexTicketKey(accountID, model))
	require.True(t, ok)
	next, ok := value.(time.Time)
	require.True(t, ok)
	return next
}

func TestCodexTicketAutomaticScheduleUsesFixedHarvestWindow(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	// 无论 TTL / refresh_before 如何配置，取得新票据后都固定为 30~60 秒后重打。
	for _, cfg := range []config.OpenAICodexTicketConfig{
		{Enabled: true, TTLSeconds: 60},
		{Enabled: true, TTLSeconds: 200, RefreshBeforeSeconds: 600},
		{Enabled: true, TTLSeconds: 3600, RefreshBeforeSeconds: 600},
	} {
		svc := ticketTestService(t, cfg, nil)
		ticket := &openAICodexTicket{AccountID: 41, Model: "gpt-6-astra", CapturedAt: now,
			ExpiresAt: now.Add(time.Duration(cfg.TTLSeconds) * time.Second)}
		svc.scheduleCodexTicketAfterSuccess(ticket)
		next := scheduledCodexTicketNext(t, svc, 41, "gpt-6-astra")
		require.GreaterOrEqual(t, next.Sub(now), codexTicketHarvestIntervalMin)
		require.LessOrEqual(t, next.Sub(now), codexTicketHarvestIntervalMax)
	}

	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 200}, nil)
	require.Equal(t, 30*time.Second, codexTicketMissingRetry)
	require.Equal(t, 30*time.Second, codexTicketValidRetryMin)
	retry := codexTicketJitter("41\x00gpt-6-astra", now, codexTicketValidRetryMin, codexTicketValidRetryMax)
	require.GreaterOrEqual(t, retry, 30*time.Second)
	require.LessOrEqual(t, retry, 40*time.Second)
	svc.openaiCodexTicketNextAttempt.Store(openAICodexTicketKey(42, "gpt-5.6-sol"), now.Add(3*time.Second))
	require.Equal(t, 3*time.Second, svc.openAICodexTicketNextScanDelay(now))
}

func TestCodexTicketAutomaticDueUsesFixedHarvestWindow(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 200}, nil)
	account := ticketTestAccount(41)
	now := time.Now().Truncate(time.Second)

	require.True(t, svc.codexTicketAutomaticDue(account, "gpt-6-astra", now), "无票据时应立即打票")

	fresh := &openAICodexTicket{AccountID: 41, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292,
		CapturedAt: now, ExpiresAt: now.Add(200 * time.Second)}
	svc.storeOpenAICodexTicket(context.Background(), account, fresh)
	svc.openaiCodexTicketNextAttempt.Delete(openAICodexTicketKey(41, "gpt-6-astra"))
	require.False(t, svc.codexTicketAutomaticDue(account, "gpt-6-astra", now))
	require.True(t, svc.codexTicketAutomaticDue(account, "gpt-6-astra", now.Add(codexTicketHarvestIntervalMax+time.Second)))

	expired := &openAICodexTicket{AccountID: 41, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292,
		CapturedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)}
	svc.storeOpenAICodexTicket(context.Background(), account, expired)
	svc.openaiCodexTicketNextAttempt.Delete(openAICodexTicketKey(41, "gpt-6-astra"))
	require.True(t, svc.codexTicketAutomaticDue(account, "gpt-6-astra", now), "过期票据应立即触发重打")
}

func TestManualCodexTicketHarvestHonorsParticipationAndAvailability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*Account, *config.OpenAICodexTicketConfig)
		prepare   func(*OpenAIGatewayService, *Account)
		want      error
	}{
		{
			name: "global switch disabled",
			configure: func(_ *Account, cfg *config.OpenAICodexTicketConfig) {
				cfg.Enabled = false
			},
			want: ErrCodexTicketUnavailable,
		},
		{
			name: "account opted out",
			configure: func(account *Account, _ *config.OpenAICodexTicketConfig) {
				account.Extra = make(map[string]any)
				account.Extra[codexTicketAccountEnabledKey] = false
			},
			want: ErrCodexTicketUnavailable,
		},
		{
			name: "model opted out",
			configure: func(account *Account, _ *config.OpenAICodexTicketConfig) {
				account.Extra = make(map[string]any)
				account.Extra[codexTicketModelsEnabledKey] = map[string]any{"gpt-6-astra": false}
			},
			want: ErrCodexTicketUnavailable,
		},
		{
			name: "no managed proxy",
			configure: func(_ *Account, cfg *config.OpenAICodexTicketConfig) {
				cfg.HarvestProxyURL = ""
			},
			want: ErrCodexTicketNoProxy,
		},
		{
			name: "same model already running",
			prepare: func(svc *OpenAIGatewayService, account *Account) {
				svc.openaiCodexTicketInFlight.Store(openAICodexTicketKey(account.ID, "gpt-6-astra"), true)
			},
			want: ErrCodexTicketBusy,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := ticketTestAccount(41)
			account.Status = StatusActive
			cfg := config.OpenAICodexTicketConfig{
				Enabled: true, HarvestProxyURL: "http://proxy.example:8080", Models: []string{"gpt-6-astra"},
			}
			if tc.configure != nil {
				tc.configure(account, &cfg)
			}
			repo := &codexTicketQuotaRepo{account: *account}
			requests := 0
			svc := ticketTestService(t, cfg, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
				requests++
				return codexTicketResponse(), nil
			}})
			svc.accountRepo = repo
			if tc.prepare != nil {
				tc.prepare(svc, account)
			}
			_, err := svc.ManualCodexTicketHarvest(context.Background(), account.ID, "gpt-6-astra")
			require.ErrorIs(t, err, tc.want)
			require.Zero(t, requests)
		})
	}
}
