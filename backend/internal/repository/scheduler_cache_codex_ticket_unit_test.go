//go:build unit

package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCachePreservesCodexTicketPolicy(t *testing.T) {
	for _, tc := range []struct {
		name          string
		globalDefault bool
		override      any
		wantAllow     bool
	}{
		{name: "allow overrides global deny", override: true, wantAllow: true},
		{name: "deny overrides global allow", globalDefault: true, override: false},
		{name: "allow with global allow", globalDefault: true, override: true, wantAllow: true},
		{name: "deny with global deny", override: false},
		{name: "inherit global allow", globalDefault: true, wantAllow: true},
		{name: "inherit global deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			account := service.Account{
				ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Status: service.StatusActive, Schedulable: true,
				Extra: map[string]any{"unrelated": "drop me"},
			}
			if tc.override != nil {
				account.Extra["codex_allow_without_ticket"] = tc.override
			}
			bucket := service.SchedulerBucket{GroupID: 2, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))

			// Candidate filtering reads the metadata snapshot, while forwarding reads
			// the full account. Both must apply the same account override.
			snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
			require.NoError(t, err)
			require.True(t, hit)
			require.Len(t, snapshot, 1)
			full, err := cache.GetAccount(ctx, account.ID)
			require.NoError(t, err)
			require.NotNil(t, full)
			for _, cached := range []*service.Account{snapshot[0], full} {
				if tc.override == nil {
					require.NotContains(t, cached.Extra, "codex_allow_without_ticket")
				} else {
					require.Equal(t, tc.override, cached.Extra["codex_allow_without_ticket"])
				}
				require.Equal(t, tc.wantAllow, service.OpenAICodexAllowsWithoutTicket(cached, tc.globalDefault))
			}
			require.NotContains(t, snapshot[0].Extra, "unrelated")
		})
	}
}

func TestSchedulerCacheRefreshesCodexTicketPolicy(t *testing.T) {
	for _, globalDefault := range []bool{false, true} {
		t.Run(fmt.Sprintf("global_allow=%t", globalDefault), func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			account := service.Account{
				ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Status: service.StatusActive, Schedulable: true,
				Extra: map[string]any{},
			}
			bucket := service.SchedulerBucket{GroupID: 2, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))

			for _, step := range []struct {
				name      string
				override  any
				wantAllow bool
			}{
				{name: "allow", override: true, wantAllow: true},
				{name: "deny", override: false},
				{name: "inherit after deny", wantAllow: globalDefault},
				{name: "allow again", override: true, wantAllow: true},
				{name: "inherit after allow", wantAllow: globalDefault},
			} {
				t.Run(step.name, func(t *testing.T) {
					if step.override == nil {
						delete(account.Extra, "codex_allow_without_ticket")
					} else {
						account.Extra["codex_allow_without_ticket"] = step.override
					}
					// Account edits refresh both payloads without rebuilding the bucket.
					require.NoError(t, cache.SetAccount(ctx, &account))
					snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
					require.NoError(t, err)
					require.True(t, hit)
					require.Len(t, snapshot, 1)
					require.Equal(t, step.wantAllow, service.OpenAICodexAllowsWithoutTicket(snapshot[0], globalDefault))
					if step.override == nil {
						require.NotContains(t, snapshot[0].Extra, "codex_allow_without_ticket")
					} else {
						require.Equal(t, step.override, snapshot[0].Extra["codex_allow_without_ticket"])
					}
				})
			}
		})
	}
}
