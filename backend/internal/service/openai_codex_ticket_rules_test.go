package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketTargetLengthByPlan(t *testing.T) {
	for _, plan := range []string{"team", "self_serve_business_prolite", "selfservebusinessprolite", " SELF_SERVE_BUSINESS_PROLITE ", "self-serve-business-pro-lite", "self serve business pro lite"} {
		t.Run(plan, func(t *testing.T) {
			account := ticketTestAccount(41)
			account.Credentials["plan_type"] = plan
			for _, configured := range []int{0, 292, 312} {
				require.Equal(t, 332, openAICodexTicketTargetLength(account, configured))
			}
		})
	}
	for _, plan := range []string{"pro", "prolite", "plus", "free", "", "unknown", "self_serve_business_usage_based"} {
		t.Run(plan, func(t *testing.T) {
			account := ticketTestAccount(41)
			account.Credentials["plan_type"] = plan
			require.Equal(t, 292, openAICodexTicketTargetLength(account, 0))
			require.True(t, openAICodexTicketResponseValid(account, 312, http.StatusOK, fakeCodexTicketState(312)))
			require.False(t, openAICodexTicketResponseValid(account, 312, http.StatusOK, fakeCodexTicketState(292)))
		})
	}
}

func TestCodexTicketQuotaResumeBoundary(t *testing.T) {
	now := time.Date(2026, 9, 20, 1, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	for _, tc := range []struct {
		name    string
		account *Account
		paused  bool
		resume  *time.Time
	}{
		{name: "no account"},
		{name: "no quota signal", account: &Account{}},
		{name: "screenshot recovery in 3h17m", account: &Account{RateLimitResetAt: at(3*time.Hour + 17*time.Minute)}, paused: true, resume: at(2*time.Hour + 47*time.Minute)},
		{name: "one nanosecond before prewarm", account: &Account{RateLimitResetAt: at(30*time.Minute + time.Nanosecond)}, paused: true, resume: at(time.Nanosecond)},
		{name: "exactly thirty minutes", account: &Account{RateLimitResetAt: at(30 * time.Minute)}, resume: at(0)},
		{name: "transient cooldown", account: &Account{RateLimitResetAt: at(5 * time.Second)}, resume: at(5*time.Second - 30*time.Minute)},
		{name: "already recovered", account: &Account{RateLimitResetAt: at(-time.Second)}},
		{name: "stale extra quota is ignored", account: &Account{Extra: map[string]any{
			"codex_5h_used_percent": 100, "codex_7d_used_percent": 100,
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := openAICodexTicketQuota(tc.account, now)
			require.Equal(t, tc.paused, got.paused)
			require.Equal(t, tc.resume, got.resumeAt)
		})
	}
}

func TestCodexTicketResponseRulesApplyToHarvestAndForwarding(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol"} {
		// 本项目仅允许付费 OAuth 账号参与打票（SetupToken 豁免），与 sub4api 的宽松口径不同。
		for _, accountType := range []string{AccountTypeOAuth} {
			for _, tc := range []struct {
				plan   string
				length int
			}{{"pro", 292}, {"prolite", 292}, {" TEAM ", 332}, {"self_serve_business_prolite", 332}} {
				for _, status := range []int{http.StatusOK, http.StatusTooManyRequests} {
					for _, trigger := range []string{"automatic", "manual"} {
						t.Run(fmt.Sprintf("%s/%s/%s/%d/%s", model, accountType, tc.plan, status, trigger), func(t *testing.T) {
							account := ticketTestAccount(41)
							account.Type = accountType
							account.Status = StatusActive
							account.Credentials["plan_type"] = tc.plan
							headers := http.Header{}
							headers.Set(openAICodexTurnStateHeader, fakeCodexTicketState(tc.length))
							upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(""))}}}
							cfg := config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, HarvestProxyURL: "http://proxy.example:8080"}
							svc := ticketTestService(t, cfg, upstream)
							repo := &codexTicketQuotaRepo{account: *account}
							history := &codexTicketAttemptMemoryRepo{}
							svc.accountRepo, svc.openaiCodexTicketHistory = repo, history
							if trigger == "manual" {
								result, err := svc.ManualCodexTicketHarvest(context.Background(), account.ID, model)
								require.NoError(t, err)
								require.Equal(t, "success", result.Outcome)
								require.True(t, result.HistoryRecorded)
								require.True(t, result.TicketStatus.Ready)
								require.Equal(t, tc.length, result.TicketStatus.ExpectedTicketLength)
							} else {
								svc.probeOnceOpenAICodexTicket(context.Background(), account, model)
							}
							require.Len(t, history.inserted, 1)
							require.Equal(t, "success", history.inserted[0].Outcome)
							require.Equal(t, trigger, history.inserted[0].Trigger)
							require.Equal(t, tc.length, *history.inserted[0].TicketLength)
							ticket := svc.lookupOpenAICodexTicket(account, model)
							require.NotNil(t, ticket)
							require.Equal(t, status, ticket.HTTPStatus)
							require.Equal(t, tc.length, ticket.Length)
							require.False(t, svc.openAICodexTicketBlocksAccount(account, model))
							forwardHeaders := http.Header{}
							require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, model, forwardHeaders))
							require.Equal(t, ticket.State, forwardHeaders.Get(openAICodexTurnStateHeader))
							// Hydration and the admin summary must agree after a restart.
							account.Extra = maps.Clone(repo.account.Extra)
							require.Contains(t, account.Extra, openAICodexTicketExtraKey(model))
							restarted := ticketTestService(t, cfg, nil)
							require.False(t, restarted.openAICodexTicketBlocksAccount(account, model))
							cfg.Models = []string{model}
							summary := OpenAICodexTicketStatuses(account, cfg, time.Now())
							require.Len(t, summary, 1)
							require.True(t, summary[0].Ready)
							require.Equal(t, tc.length, summary[0].ExpectedTicketLength)
						})
					}
				}
			}
		}
	}
}

func TestCodexTicketRejectsInvalidHeadersEvenOn429(t *testing.T) {
	for _, tc := range []struct {
		name, plan, state string
		status            int
	}{
		{"missing", "pro", "", 429},
		{"wrong length", "pro", fakeCodexTicketState(312), 429},
		{"wrong prefix", "pro", strings.Repeat("X", 292), 429},
		{"team requires 332", "team", fakeCodexTicketState(292), 429},
		{"personal requires 292", "pro", fakeCodexTicketState(332), 429},
		{"personal prolite requires 292", "prolite", fakeCodexTicketState(332), 429},
		{"business premium requires 332", "self_serve_business_prolite", fakeCodexTicketState(292), 429},
		{"business premium wrong prefix", "self_serve_business_prolite", strings.Repeat("X", 332), 429},
		{"business premium unauthorized", "self_serve_business_prolite", fakeCodexTicketState(332), 401},
		{"business premium server error", "self_serve_business_prolite", fakeCodexTicketState(332), 503},
		{"unauthorized", "pro", fakeCodexTicketState(292), 401},
		{"server error", "pro", fakeCodexTicketState(292), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := ticketTestAccount(41)
			account.Credentials["plan_type"] = tc.plan
			headers := http.Header{}
			headers.Set(openAICodexTurnStateHeader, tc.state)
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example:8080"}, &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: tc.status, Header: headers}}})
			svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
			require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
		})
	}
}

type codexTicketQuotaRepo struct {
	AccountRepository
	mu         sync.Mutex
	account    Account
	persistErr error
}

func (r *codexTicketQuotaRepo) ListByPlatform(context.Context, string) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.account
	account.Extra = maps.Clone(account.Extra)
	return []Account{account}, nil
}
func (r *codexTicketQuotaRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.persistErr != nil {
		return r.persistErr
	}
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	maps.Copy(r.account.Extra, updates)
	return nil
}
func TestCodexTicketHarvestNeverMutatesRateLimitStateButKeeps429Ticket(t *testing.T) {
	for _, bodyOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("body_reset_%t", bodyOnly), func(t *testing.T) {
			account := ticketTestAccount(41)
			account.Status, account.Schedulable = StatusActive, true
			repo := &codexTicketQuotaRepo{account: *account}
			headers := http.Header{}
			headers.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
			originalReset := time.Now().Add(20 * time.Minute).Truncate(time.Second)
			account.RateLimitResetAt = &originalReset
			repo.account.RateLimitResetAt = &originalReset
			upstreamReset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
			body := ""
			if bodyOnly {
				headers.Set("Content-Type", "application/json")
				body = fmt.Sprintf(`{"error":{"type":"usage_limit_reached","resets_at":%d}}`, upstreamReset.Unix())
			} else {
				headers.Set("X-Codex-Primary-Used-Percent", "100")
				headers.Set("X-Codex-Primary-Window-Minutes", "300")
				headers.Set("X-Codex-Primary-Reset-After-Seconds", "10800")
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 429, Header: headers, Body: io.NopCloser(strings.NewReader(body))}}}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example:8080", Models: []string{"gpt-6-astra"}}, upstream)
			svc.accountRepo = repo
			svc.refreshOpenAICodexTickets(context.Background())
			require.NotNil(t, repo.account.RateLimitResetAt)
			require.Equal(t, originalReset, *repo.account.RateLimitResetAt)
			require.False(t, repo.account.IsSchedulable(), "a harvested ticket must not restore quota")
			ticket := svc.lookupOpenAICodexTicket(&repo.account, "gpt-6-astra")
			require.NotNil(t, ticket)
			require.Equal(t, 429, ticket.HTTPStatus)
		})
	}
}

func TestCodexTicketQuotaPersistenceFailureDoesNotLoseTicket(t *testing.T) {
	account := ticketTestAccount(41)
	repo := &codexTicketQuotaRepo{account: *account, persistErr: errors.New("database unavailable")}
	upstream := &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		resp := codexTicketResponse()
		resp.StatusCode = 429
		resp.Header.Set("X-Codex-Secondary-Used-Percent", "100")
		resp.Header.Set("X-Codex-Secondary-Reset-After-Seconds", "7200")
		return resp, nil
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example:8080"}, upstream)
	svc.accountRepo = repo
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	ticket := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, ticket)
	require.True(t, ticket.valid(time.Now(), 292))
}

func TestCodexTicketTransient429DoesNotCreateQuotaPause(t *testing.T) {
	account := ticketTestAccount(41)
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example:8080"}, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		resp := codexTicketResponse()
		resp.StatusCode = 429
		resp.Header.Set("X-Codex-Secondary-Used-Percent", "25")
		resp.Header.Set("X-Codex-Secondary-Reset-After-Seconds", "7200")
		return resp, nil
	}})
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Nil(t, account.RateLimitResetAt)
	require.False(t, openAICodexTicketQuota(account, time.Now()).paused)
	require.NotNil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
}

func TestCodexTicketHarvesterResumesBeforeQuotaRecovery(t *testing.T) {
	account := ticketTestAccount(245)
	account.Status, account.Schedulable = StatusActive, true
	resetAt := time.Now().Add(3*time.Hour + 17*time.Minute)
	account.RateLimitResetAt = &resetAt
	repo := &codexTicketQuotaRepo{account: *account}
	var requests atomic.Int64
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: true, HarvestProxyURL: "http://proxy.example:8080", Models: []string{"gpt-6-astra", "gpt-5.6-sol"},
	}, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return codexTicketResponse(), nil
	}})
	svc.accountRepo = repo
	svc.refreshOpenAICodexTickets(context.Background())
	require.Zero(t, requests.Load(), "neither model should probe before the prewarm window")
	summary := OpenAICodexTicketStatuses(account, svc.openAICodexTicketConfig(), time.Now())
	require.Len(t, summary, 2)
	for _, model := range summary {
		require.True(t, model.HarvestPaused)
		require.Equal(t, resetAt, *model.QuotaResetAt)
		require.Equal(t, resetAt.Add(-30*time.Minute), *model.HarvestResumeAt)
	}

	resetAt = time.Now().Add(29 * time.Minute)
	repo.account.RateLimitResetAt = &resetAt
	svc.refreshOpenAICodexTickets(context.Background())
	require.Equal(t, int64(2), requests.Load(), "both models should prewarm while business quota is still limited")
	require.False(t, repo.account.IsSchedulable())
	for _, model := range svc.openAICodexTicketConfig().Models {
		require.NotNil(t, svc.lookupOpenAICodexTicket(&repo.account, model))
	}
}

type codexTicketSlowErrorBody struct {
	ctx    context.Context
	closed bool
}

func (b *codexTicketSlowErrorBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *codexTicketSlowErrorBody) Close() error { b.closed = true; return nil }

func TestCodexTicketAcceptsHeaderWhen429BodyTimesOut(t *testing.T) {
	var body *codexTicketSlowErrorBody
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, &codexTicketFuncUpstream{do: func(req *http.Request) (*http.Response, error) {
		body = &codexTicketSlowErrorBody{ctx: req.Context()}
		response := codexTicketResponse()
		response.StatusCode = http.StatusTooManyRequests
		response.Header.Set("Content-Type", "application/json")
		response.Body = body
		return response, nil
	}})
	account := ticketTestAccount(41)
	state, _, status, err := svc.fireOpenAICodexTicketProbe(context.Background(), account, "test-token", "gpt-6-astra", "", 20*time.Millisecond)
	require.NoError(t, err)
	require.True(t, body.closed)
	require.True(t, openAICodexTicketResponseValid(account, 292, status, state))
}
