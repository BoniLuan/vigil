-- +goose Up
ALTER TABLE monitors ADD COLUMN next_check_at timestamptz;
UPDATE monitors
SET next_check_at = transaction_timestamp()
WHERE enabled AND archived_at IS NULL;

CREATE TABLE scheduled_executions (
    id uuid PRIMARY KEY,
    monitor_id uuid NOT NULL REFERENCES monitors(id) ON DELETE RESTRICT,
    scheduled_at timestamptz NOT NULL,
    status varchar(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'claimed', 'completed', 'skipped')),
    lease_owner uuid,
    lease_expires_at timestamptz,
    claim_count integer NOT NULL DEFAULT 0 CHECK (claim_count >= 0),
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT scheduled_executions_identity UNIQUE (monitor_id, scheduled_at),
    CONSTRAINT scheduled_executions_state_fields CHECK (
        (status = 'pending' AND lease_owner IS NULL AND lease_expires_at IS NULL AND finished_at IS NULL) OR
        (status = 'claimed' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL AND finished_at IS NULL) OR
        (status IN ('completed', 'skipped') AND lease_owner IS NULL AND lease_expires_at IS NULL AND finished_at IS NOT NULL)
    )
);

CREATE INDEX scheduled_executions_claimable_idx
    ON scheduled_executions (scheduled_at, id)
    WHERE status IN ('pending', 'claimed');
CREATE INDEX monitors_next_check_idx
    ON monitors (next_check_at, id)
    WHERE enabled AND archived_at IS NULL;

ALTER TABLE check_results
    ADD COLUMN execution_id uuid UNIQUE REFERENCES scheduled_executions(id) ON DELETE RESTRICT;

ALTER TABLE monitor_states
    ADD COLUMN last_applied_scheduled_at timestamptz;

-- +goose Down
ALTER TABLE monitor_states DROP COLUMN last_applied_scheduled_at;
ALTER TABLE check_results DROP COLUMN execution_id;
DROP TABLE scheduled_executions;
DROP INDEX monitors_next_check_idx;
ALTER TABLE monitors DROP COLUMN next_check_at;
