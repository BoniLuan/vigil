-- +goose Up
ALTER TABLE monitors
    ADD COLUMN archived_at timestamptz;

CREATE TABLE check_results (
    id uuid PRIMARY KEY,
    monitor_id uuid NOT NULL REFERENCES monitors(id) ON DELETE RESTRICT,
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL,
    duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
    outcome varchar(32) NOT NULL CHECK (outcome IN (
        'success', 'http_failure', 'timeout', 'dns_error',
        'tls_error', 'connection_error', 'internal_error'
    )),
    status_code smallint CHECK (status_code BETWEEN 100 AND 599),
    error_code varchar(64),
    error_description varchar(512),
    dialed_ip inet,
    tls_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT check_results_time_order CHECK (finished_at >= started_at),
    CONSTRAINT check_results_error_code CHECK (error_code IS NULL OR error_code IN (
        'destination_prohibited', 'dns_lookup_failed', 'dns_no_addresses',
        'request_timeout', 'deadline_exceeded', 'cancelled',
        'tls_certificate', 'tls_hostname', 'tls_handshake_failed',
        'connection_failed', 'connection_refused', 'connection_reset',
        'connection_closed', 'network_unreachable', 'unexpected_status',
        'redirect_limit', 'redirect_loop', 'redirect_downgrade',
        'redirect_invalid', 'internal_error'
    )),
    CONSTRAINT check_results_error_description_bytes CHECK (
        error_description IS NULL OR octet_length(error_description) <= 512
    ),
    CONSTRAINT check_results_error_pair CHECK ((error_code IS NULL) = (error_description IS NULL)),
    CONSTRAINT check_results_outcome_error CHECK (
        (outcome = 'success' AND error_code IS NULL) OR
        (outcome <> 'success' AND error_code IS NOT NULL)
    ),
    CONSTRAINT check_results_http_failure_status CHECK (
        outcome <> 'http_failure' OR status_code IS NOT NULL
    )
);

CREATE INDEX check_results_monitor_started_idx
    ON check_results (monitor_id, started_at DESC, id DESC);
CREATE INDEX check_results_started_idx ON check_results (started_at);
CREATE INDEX monitors_archived_at_idx ON monitors (archived_at) WHERE archived_at IS NOT NULL;

ALTER TABLE monitor_states
    ADD COLUMN last_check_result_id uuid REFERENCES check_results(id) ON DELETE SET NULL,
    ADD COLUMN last_checked_at timestamptz,
    ADD COLUMN last_outcome varchar(32) CHECK (last_outcome IN (
        'success', 'http_failure', 'timeout', 'dns_error',
        'tls_error', 'connection_error', 'internal_error'
    )),
    ADD COLUMN last_status_code smallint CHECK (last_status_code BETWEEN 100 AND 599),
    ADD COLUMN last_duration_ms bigint CHECK (last_duration_ms >= 0),
    ADD COLUMN consecutive_failures bigint NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    ADD COLUMN consecutive_successes bigint NOT NULL DEFAULT 0 CHECK (consecutive_successes >= 0);

-- +goose Down
ALTER TABLE monitor_states
    DROP COLUMN consecutive_successes,
    DROP COLUMN consecutive_failures,
    DROP COLUMN last_duration_ms,
    DROP COLUMN last_status_code,
    DROP COLUMN last_outcome,
    DROP COLUMN last_checked_at,
    DROP COLUMN last_check_result_id;

DROP INDEX monitors_archived_at_idx;
DROP TABLE check_results;
ALTER TABLE monitors DROP COLUMN archived_at;
