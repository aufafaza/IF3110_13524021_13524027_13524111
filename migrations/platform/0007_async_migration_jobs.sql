CREATE TABLE migration_async_jobs (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id      TEXT NOT NULL UNIQUE,
    execution_id    UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    actor_id        BIGINT NOT NULL,
    database_id     BIGINT NOT NULL REFERENCES managed_databases(id),
    migration_id    BIGINT NOT NULL,
    revision        TEXT NOT NULL,
    operation       TEXT NOT NULL CHECK (operation = 'apply'),
    traceparent     TEXT NOT NULL,
    tracestate      TEXT NOT NULL,
    state           TEXT NOT NULL DEFAULT 'queued'
                    CHECK (state IN ('queued', 'running', 'reconciling', 'reporting', 'retrying', 'succeeded', 'failed')),
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    attempt_limit   INTEGER NOT NULL DEFAULT 3,
    failure_code    TEXT,
    owns_database   BOOLEAN NOT NULL DEFAULT false,
    eligible_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    claim_token     UUID,
    lease_until     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((claim_token IS NULL) = (lease_until IS NULL)),
    CHECK (attempt_count BETWEEN 0 AND attempt_limit)
);

CREATE UNIQUE INDEX migration_async_database_owner
    ON migration_async_jobs (database_id) WHERE owns_database;
CREATE INDEX migration_async_database_queue
    ON migration_async_jobs (database_id, id) WHERE state NOT IN ('succeeded', 'failed');
CREATE INDEX migration_async_jobs_ready
    ON migration_async_jobs (eligible_at, id) WHERE state NOT IN ('succeeded', 'failed');

CREATE TABLE migration_async_attempts (
    job_id          BIGINT NOT NULL REFERENCES migration_async_jobs(id),
    number          INTEGER NOT NULL,
    id              UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    target_id       UUID NOT NULL,
    plan_hash       TEXT NOT NULL,
    sql_snapshot    TEXT NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at     TIMESTAMPTZ,
    failure_code    TEXT,
    retryable       BOOLEAN NOT NULL DEFAULT false,
    delivered_at    TIMESTAMPTZ,
    PRIMARY KEY (job_id, number)
);
