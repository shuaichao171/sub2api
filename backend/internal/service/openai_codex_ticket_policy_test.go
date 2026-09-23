package service

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketPolicyMatrix(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, override := range []any{nil, false, true} {
			for _, state := range []string{"missing", "expired", "valid"} {
				t.Run(fmt.Sprintf("global=%v/override=%v/%s", global, override, state), func(t *testing.T) {
					now := time.Now()
					cfg := config.OpenAICodexTicketConfig{Enabled: true, FailClosed: !global, Models: []string{"gpt-6-astra"}}
					svc := ticketTestService(t, cfg, nil)
					account := ticketTestAccount(41)
					account.Extra = map[string]any{"codex_allow_without_ticket": override}
					if state != "missing" {
						expires := now.Add(time.Hour)
						if state == "expired" {
							expires = now.Add(-time.Second)
						}
						ticket := &openAICodexTicket{
							AccountID: account.ID, Model: "gpt-6-astra", State: fakeCodexTicketState(292),
							Length: 292, CapturedAt: now.Add(-time.Minute), ExpiresAt: expires,
						}
						account.Extra[openAICodexTicketExtraKey("gpt-6-astra")] = ticket
						svc.storeOpenAICodexTicket(context.Background(), account, ticket)
					}
					allow := global
					if value, ok := override.(bool); ok {
						allow = value
					}
					blocked := !allow && state != "valid"
					require.Equal(t, blocked, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
					require.Equal(t, blocked, OpenAICodexTicketStatuses(account, cfg, now)[0].Blocked)
					headers := http.Header{}
					err := svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers)
					if blocked {
						require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
					} else {
						require.NoError(t, err)
					}
					if state == "valid" {
						require.Equal(t, fakeCodexTicketState(292), headers.Get(openAICodexTurnStateHeader))
					} else {
						require.Empty(t, headers)
					}
				})
			}
		}
	}
}

func TestCodexTicketGlobalPolicyHotReloadAndAccountOverride(t *testing.T) {
	ctx := context.Background()
	repo := &codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{}}}
	settings := NewSettingService(repo, nil)
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	svc.settingService = settings
	account := ticketTestAccount(41)
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	for _, allow := range []bool{true, false, true} {
		repo.values[SettingKeyOpenAICodexTicketAllowWithoutTicket] = strconv.FormatBool(allow)
		settings.InvalidateOpenAICodexTicketAllowWithoutTicketCache()
		require.Equal(t, allow, settings.GetOpenAICodexTicketAllowWithoutTicket(ctx, !allow))
		require.Equal(t, !allow, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	}
	account.Extra = map[string]any{"codex_allow_without_ticket": false}
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	delete(account.Extra, "codex_allow_without_ticket")
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	repo.values[SettingKeyOpenAICodexTicketEnabled] = "false"
	settings.InvalidateOpenAICodexTicketEnabledCache()
	account.Extra["codex_allow_without_ticket"] = false
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
}
