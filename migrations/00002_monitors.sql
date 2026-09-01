-- +goose Up
CREATE TABLE monitors (
    id uuid PRIMARY KEY,
    name varchar(200) NOT NULL CHECK (btrim(name) <> ''),
    slug varchar(100) NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    description text,
    kind varchar(20) NOT NULL CHECK (kind IN ('http')),
    url text NOT NULL,
    http_method varchar(10) NOT NULL CHECK (http_method IN ('GET', 'HEAD')),
    expected_status_min smallint NOT NULL CHECK (expected_status_min BETWEEN 100 AND 599),
    expected_status_max smallint NOT NULL CHECK (expected_status_max BETWEEN 100 AND 599),
    interval_seconds integer NOT NULL CHECK (interval_seconds BETWEEN 10 AND 86400),
    timeout_ms integer NOT NULL CHECK (timeout_ms BETWEEN 100 AND 30000),
    failure_threshold smallint NOT NULL CHECK (failure_threshold BETWEEN 1 AND 100),
    recovery_threshold smallint NOT NULL CHECK (recovery_threshold BETWEEN 1 AND 100),
    enabled boolean NOT NULL DEFAULT true,
    public boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT monitors_expected_status_range CHECK (expected_status_min <= expected_status_max),
    CONSTRAINT monitors_timeout_less_than_interval CHECK (timeout_ms < interval_seconds * 1000)
);

CREATE TABLE monitor_states (
    monitor_id uuid PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    state varchar(20) NOT NULL CHECK (state IN ('pending', 'up', 'down', 'paused')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX monitors_enabled_idx ON monitors (id) WHERE enabled;
CREATE INDEX monitors_created_at_idx ON monitors (created_at DESC, id DESC);

-- +goose Down
DROP TABLE monitor_states;
DROP TABLE monitors;
