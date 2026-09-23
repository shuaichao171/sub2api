package service

import (
	"context"
	"time"
)

// CodexTicketAttempt is deliberately limited to metadata, never the ticket,
// credentials or a proxy URL.
type CodexTicketAttempt struct {
	ID           int64      `json:"id"`
	AccountID    int64      `json:"account_id"`
	Model        string     `json:"model"`
	OccurredAt   time.Time  `json:"occurred_at"`
	Outcome      string     `json:"outcome"`
	Trigger      string     `json:"trigger"`
	HTTPStatus   *int       `json:"http_status"`
	TicketLength *int       `json:"ticket_length"`
	DurationMS   int        `json:"duration_ms"`
	ReasonCode   string     `json:"reason_code,omitempty"`
	ProxyID      *int64     `json:"proxy_id,omitempty"`
	ProxyName    string     `json:"proxy_name,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type CodexTicketAttemptRepository interface {
	Insert(context.Context, *CodexTicketAttempt) error
	List(context.Context, int64, string, bool, int, int) ([]CodexTicketAttempt, int64, error)
	Cleanup(context.Context) error
	TryLock(context.Context, int64, string) (func(), bool, error)
}
