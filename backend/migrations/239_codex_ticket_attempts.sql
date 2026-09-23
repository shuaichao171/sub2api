-- Ticket material and proxy credentials must never enter this audit table.
CREATE SEQUENCE IF NOT EXISTS codex_ticket_attempts_id_seq;

CREATE TABLE IF NOT EXISTS codex_ticket_attempts (
    id BIGINT NOT NULL DEFAULT nextval('codex_ticket_attempts_id_seq'),
    account_id BIGINT NOT NULL,
    model TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'miss', 'error')),
    source TEXT NOT NULL CHECK (source IN ('automatic', 'manual')),
    http_status INTEGER,
    ticket_length INTEGER,
    duration_ms INTEGER,
    reason_code TEXT,
    proxy_id BIGINT,
    proxy_name TEXT,
    expires_at TIMESTAMPTZ,
    PRIMARY KEY (outcome, occurred_at, id)
) PARTITION BY LIST (outcome);

CREATE TABLE IF NOT EXISTS codex_ticket_attempts_success
    PARTITION OF codex_ticket_attempts FOR VALUES IN ('success');
CREATE TABLE IF NOT EXISTS codex_ticket_attempts_failed
    PARTITION OF codex_ticket_attempts FOR VALUES IN ('miss', 'error')
    PARTITION BY RANGE (occurred_at);

DO $$
DECLARE
    d DATE;
    partition_name TEXT;
BEGIN
    FOR d IN SELECT generate_series(
        ((NOW() AT TIME ZONE 'UTC')::date - 31),
        ((NOW() AT TIME ZONE 'UTC')::date + 7),
        INTERVAL '1 day'
    )::date LOOP
        partition_name := 'codex_ticket_attempts_failed_' || to_char(d, 'YYYYMMDD');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF codex_ticket_attempts_failed FOR VALUES FROM (%L) TO (%L)',
            partition_name, (d::timestamp AT TIME ZONE 'UTC'), ((d + 1)::timestamp AT TIME ZONE 'UTC')
        );
    END LOOP;
END $$;

CREATE INDEX IF NOT EXISTS codex_ticket_attempts_account_model_time
    ON codex_ticket_attempts (account_id, model, occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS codex_ticket_attempts_success_time
    ON codex_ticket_attempts (account_id, model, occurred_at DESC, id DESC)
    WHERE outcome = 'success';
CREATE INDEX IF NOT EXISTS codex_ticket_attempts_failure_cleanup
    ON codex_ticket_attempts (occurred_at, id)
    WHERE outcome <> 'success';
