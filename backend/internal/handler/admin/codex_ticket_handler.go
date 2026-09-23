package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func codexTicketAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}

func codexTicketError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrCodexTicketBusy):
		response.ErrorWithDetails(c, http.StatusConflict, "Ticket attempt already running", "CODEX_TICKET_BUSY", nil)
	case errors.Is(err, service.ErrCodexTicketNoProxy):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, "No available ticket proxy", "CODEX_TICKET_NO_PROXY", nil)
	case errors.Is(err, service.ErrCodexTicketModel):
		response.ErrorWithDetails(c, http.StatusBadRequest, "Unsupported ticket model", "CODEX_TICKET_MODEL", nil)
	case errors.Is(err, service.ErrCodexTicketUnavailable):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, "Ticket harvesting disabled or account ineligible", "CODEX_TICKET_UNAVAILABLE", nil)
	default:
		response.ErrorFrom(c, err)
	}
}

func (h *AccountHandler) GetCodexTicketHistory(c *gin.Context) {
	id, ok := codexTicketAccountID(c)
	if !ok {
		return
	}
	model := c.Query("model")
	filter := c.DefaultQuery("filter", "all")
	if filter != "all" && filter != "success" {
		response.BadRequest(c, "filter must be all or success")
		return
	}
	page, size := response.ParsePagination(c)
	if size > 100 {
		size = 100
	}
	if h.codexTicketGateway == nil {
		codexTicketError(c, service.ErrCodexTicketUnavailable)
		return
	}
	rows, total, status, err := h.codexTicketGateway.CodexTicketHistory(c.Request.Context(), id, model, filter == "success", page, size)
	if err != nil {
		codexTicketError(c, err)
		return
	}
	available, reason := status.HarvestEnabled, ""
	if !available {
		reason = "disabled"
	}
	if available && h.codexTicketSettings != nil {
		proxies, err := h.codexTicketSettings.AvailableCodexTicketProxies(c.Request.Context())
		if err != nil || len(proxies) == 0 {
			available, reason = false, "no_proxy"
		}
	}
	response.Success(c, gin.H{"items": rows, "total": total, "page": page, "page_size": size,
		"ticket_status": status, "manual_available": available, "manual_unavailable_reason": reason})
}

func (h *AccountHandler) HarvestCodexTicket(c *gin.Context) {
	id, ok := codexTicketAccountID(c)
	if !ok {
		return
	}
	var input struct {
		Model string `json:"model" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "model is required")
		return
	}
	if h.codexTicketGateway == nil {
		codexTicketError(c, service.ErrCodexTicketUnavailable)
		return
	}
	result, err := h.codexTicketGateway.ManualCodexTicketHarvest(c.Request.Context(), id, input.Model)
	if err != nil {
		codexTicketError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) SetCodexTicketParticipation(c *gin.Context) {
	id, ok := codexTicketAccountID(c)
	if !ok {
		return
	}
	var input struct {
		Enabled *bool           `json:"enabled" binding:"required"`
		Models  map[string]bool `json:"models" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "enabled and models are required")
		return
	}
	if h.codexTicketGateway == nil {
		codexTicketError(c, service.ErrCodexTicketUnavailable)
		return
	}
	if err := h.codexTicketGateway.SetCodexTicketParticipation(c.Request.Context(), id, *input.Enabled, input.Models); err != nil {
		codexTicketError(c, err)
		return
	}
	response.Success(c, gin.H{"enabled": *input.Enabled, "models": input.Models})
}

func (h *ProxyHandler) GetCodexTicketPool(c *gin.Context) {
	if h.codexTicketSettings == nil {
		codexTicketError(c, service.ErrCodexTicketUnavailable)
		return
	}
	pool, err := h.codexTicketSettings.GetCodexTicketPool(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *ProxyHandler) UpdateCodexTicketPool(c *gin.Context) {
	if h.codexTicketSettings == nil {
		codexTicketError(c, service.ErrCodexTicketUnavailable)
		return
	}
	var pool service.CodexTicketPool
	if err := c.ShouldBindJSON(&pool); err != nil {
		response.BadRequest(c, "Invalid proxy pool")
		return
	}
	if err := h.codexTicketSettings.SetCodexTicketPool(c.Request.Context(), pool); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	saved, err := h.codexTicketSettings.GetCodexTicketPool(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}
