package repository

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"regexp"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type codexTicketAttemptRepository struct{ db *sql.DB }

const codexTicketMaintenanceLock int64 = 0x434f444558544b54

var codexTicketFailurePartitionPattern = regexp.MustCompile(`^codex_ticket_attempts_failed_(\d{8})$`)

func NewCodexTicketAttemptRepository(db *sql.DB) service.CodexTicketAttemptRepository {
	return &codexTicketAttemptRepository{db: db}
}

func (r *codexTicketAttemptRepository) Insert(ctx context.Context, a *service.CodexTicketAttempt) error {
	return r.db.QueryRowContext(ctx, `INSERT INTO codex_ticket_attempts
		(account_id,model,occurred_at,outcome,source,http_status,ticket_length,duration_ms,reason_code,proxy_id,proxy_name,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		a.AccountID, a.Model, a.OccurredAt, a.Outcome, a.Trigger, a.HTTPStatus, a.TicketLength,
		a.DurationMS, a.ReasonCode, a.ProxyID, a.ProxyName, a.ExpiresAt).Scan(&a.ID)
}

func (r *codexTicketAttemptRepository) List(ctx context.Context, accountID int64, model string, successOnly bool, page, size int) ([]service.CodexTicketAttempt, int64, error) {
	condition := "account_id=$1 AND model=$2 AND (outcome='success' OR occurred_at >= NOW() - INTERVAL '30 days')"
	if successOnly {
		condition += " AND outcome='success'"
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM codex_ticket_attempts WHERE "+condition, accountID, model).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,occurred_at,outcome,source,http_status,ticket_length,duration_ms,reason_code,proxy_id,proxy_name,expires_at
		FROM codex_ticket_attempts WHERE `+condition+` ORDER BY occurred_at DESC,id DESC LIMIT $3 OFFSET $4`,
		accountID, model, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]service.CodexTicketAttempt, 0, size)
	for rows.Next() {
		a := service.CodexTicketAttempt{AccountID: accountID, Model: model}
		var status, length, proxyID sql.NullInt64
		var reason, name sql.NullString
		var expires sql.NullTime
		if err := rows.Scan(&a.ID, &a.OccurredAt, &a.Outcome, &a.Trigger, &status, &length,
			&a.DurationMS, &reason, &proxyID, &name, &expires); err != nil {
			return nil, 0, err
		}
		if status.Valid {
			v := int(status.Int64)
			a.HTTPStatus = &v
		}
		if length.Valid {
			v := int(length.Int64)
			a.TicketLength = &v
		}
		if proxyID.Valid {
			v := proxyID.Int64
			a.ProxyID = &v
		}
		if expires.Valid {
			v := expires.Time
			a.ExpiresAt = &v
		}
		a.ReasonCode, a.ProxyName = reason.String, name.String
		result = append(result, a)
	}
	return result, total, rows.Err()
}

func (r *codexTicketAttemptRepository) Cleanup(ctx context.Context) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", codexTicketMaintenanceLock).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var unlocked bool
		_ = conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock($1)", codexTicketMaintenanceLock).Scan(&unlocked)
	}()
	if _, err := conn.ExecContext(ctx, "SET lock_timeout = '2s'"); err != nil {
		return err
	}
	defer func() {
		resetCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(resetCtx, "RESET lock_timeout")
	}()
	now := time.Now().UTC()
	for offset := 0; offset <= 7; offset++ {
		day := time.Date(now.Year(), now.Month(), now.Day()+offset, 0, 0, 0, 0, time.UTC)
		name := "codex_ticket_attempts_failed_" + day.Format("20060102")
		statement := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF codex_ticket_attempts_failed
			FOR VALUES FROM ('%s') TO ('%s')`, name, day.Format(time.RFC3339), day.Add(24*time.Hour).Format(time.RFC3339))
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create ticket attempt partition: %w", err)
		}
	}
	rows, err := conn.QueryContext(ctx, `SELECT child.relname FROM pg_inherits
		JOIN pg_class parent ON pg_inherits.inhparent=parent.oid
		JOIN pg_class child ON pg_inherits.inhrelid=child.oid
		JOIN pg_namespace parent_ns ON parent.relnamespace=parent_ns.oid
		JOIN pg_namespace child_ns ON child.relnamespace=child_ns.oid
		WHERE parent.relname='codex_ticket_attempts_failed'
		AND parent_ns.nspname=current_schema()
		AND child_ns.nspname=current_schema()`)
	if err != nil {
		return err
	}
	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	for _, name := range names {
		matches := codexTicketFailurePartitionPattern.FindStringSubmatch(name)
		if len(matches) != 2 {
			continue
		}
		day, err := time.Parse("20060102", matches[1])
		if err != nil || day.Add(24*time.Hour).After(cutoff) {
			continue
		}
		if _, err := conn.ExecContext(ctx, "DROP TABLE "+name); err != nil {
			return fmt.Errorf("drop ticket attempt partition: %w", err)
		}
	}
	// Batch-delete only the expired rows in the UTC boundary partition.
	for {
		result, err := conn.ExecContext(ctx, `DELETE FROM codex_ticket_attempts WHERE (outcome,occurred_at,id) IN (
			SELECT outcome,occurred_at,id FROM codex_ticket_attempts WHERE outcome <> 'success'
			AND occurred_at < NOW() - INTERVAL '30 days' ORDER BY occurred_at,id LIMIT 1000)`)
		if err != nil {
			return fmt.Errorf("cleanup ticket attempts: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil || n < 1000 {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (r *codexTicketAttemptRepository) TryLock(ctx context.Context, accountID int64, model string) (func(), bool, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "codex-ticket:%d:%s", accountID, model)
	key := int64(h.Sum64())
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		_ = conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked)
		_ = conn.Close()
	}, true, nil
}
