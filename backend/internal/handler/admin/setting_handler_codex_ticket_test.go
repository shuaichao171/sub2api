package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsCodexTicketProxyWriteReadAndHotReload(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	oldProxy := "http://user:old-secret@old.example.com:8080"
	newProxy := "socks5h://user:new-secret@new.example.com:1080"
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: oldProxy})
	require.Equal(t, oldProxy, h.settingService.GetOpenAICodexTicketHarvestProxyURL(context.Background()))
	rec := doUpdateSettings(t, h, map[string]any{key: newProxy}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, newProxy, repo.values[key])
	require.Equal(t, newProxy, h.settingService.GetOpenAICodexTicketHarvestProxyURL(context.Background()))
	require.NotContains(t, rec.Body.String(), "new-secret")
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_harvest_proxy_configured":true`)
	// Omission, empty input and the masked GET value all preserve the real secret.
	for _, body := range []map[string]any{{"site_name": "updated"}, {key: ""}, {key: service.MaskProxyURL(newProxy)}} {
		rec = doUpdateSettings(t, h, body, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, newProxy, repo.values[key])
	}
	get := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(get)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, get.Code)
	require.NotContains(t, get.Body.String(), "new-secret")
	require.Contains(t, get.Body.String(), "new.example.com")
}

func TestSettingsCodexTicketRejectInvalidProxyWithoutLeakingPassword(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: "http://previous.example.com:8080"})
	rec := doUpdateSettings(t, h, map[string]any{key: "ftp://user:invalid-secret@proxy.example.com:21"}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "invalid-secret")
	require.Equal(t, "http://previous.example.com:8080", repo.values[key])
}

func TestSettingsCodexTicketTTLAndReuseRoundTrip(t *testing.T) {
	ttlKey := service.SettingKeyOpenAICodexTicketTTLSeconds
	reuseKey := service.SettingKeyOpenAICodexTicketReuseExpired
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{ttlKey: "200", reuseKey: "true"})
	require.Equal(t, 200, h.settingService.GetOpenAICodexTicketTTLSeconds(context.Background(), 0))
	require.True(t, h.settingService.GetOpenAICodexTicketReuseExpired(context.Background(), false))

	rec := doUpdateSettings(t, h, map[string]any{ttlKey: 120, reuseKey: false}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "120", repo.values[ttlKey])
	require.Equal(t, "false", repo.values[reuseKey])
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_ttl_seconds":120`)
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_reuse_expired":false`)

	// 低于 60 秒必须拒绝，且不覆盖已保存值。
	rec = doUpdateSettings(t, h, map[string]any{ttlKey: 30}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "120", repo.values[ttlKey])
}

func TestSettingsCodexTicketReuseWindowRoundTrip(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketReuseExpiredMaxSeconds
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: "600"})
	require.Equal(t, 600, h.settingService.GetOpenAICodexTicketReuseExpiredMaxSeconds(context.Background(), 0))

	rec := doUpdateSettings(t, h, map[string]any{key: 0}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "0", repo.values[key])
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_reuse_expired_max_seconds":0`)

	rec = doUpdateSettings(t, h, map[string]any{key: -1}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "0", repo.values[key])

	rec = doUpdateSettings(t, h, map[string]any{key: 999999}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "0", repo.values[key])
}

func TestSettingsCodexTicketAllowWithoutTicketRoundTrip(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketAllowWithoutTicket
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: "false"})
	require.False(t, h.settingService.GetOpenAICodexTicketAllowWithoutTicket(context.Background(), true))
	rec := doUpdateSettings(t, h, map[string]any{key: true}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[key])
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_allow_without_ticket":true`)
	require.True(t, h.settingService.GetOpenAICodexTicketAllowWithoutTicket(context.Background(), false))
	rec = doUpdateSettings(t, h, map[string]any{}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[key], "omitting the field preserves the saved policy")
	rec = doUpdateSettings(t, h, map[string]any{key: false}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.False(t, h.settingService.GetOpenAICodexTicketAllowWithoutTicket(context.Background(), true))
}
