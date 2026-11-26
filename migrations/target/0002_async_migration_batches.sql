-- Run once as the migration_worker schema owner. Grant the migration login
-- SELECT, INSERT and UPDATE on batch_runs and SELECT, INSERT on the other tables.
BEGIN;
CREATE TABLE migration_worker.batch_runs (
    execution_id UUID PRIMARY KEY,
    attempt_id UUID NOT NULL,
    plan_hash TEXT NOT NULL,
    scope_rows BIGINT NOT NULL DEFAULT 0,
    committed_batches BIGINT NOT NULL DEFAULT 0,
    committed_rows BIGINT NOT NULL DEFAULT 0,
    last_key BIGINT,
    CHECK (scope_rows >= committed_rows AND committed_rows >= committed_batches AND committed_batches >= 0),
    CHECK ((committed_batches = 0) = (last_key IS NULL))
);

-- The worker creates the parent and inserts the full scope in the same
-- transaction. Avoid a foreign-key lookup for every materialized key.
CREATE TABLE migration_worker.batch_scope (
    execution_id UUID NOT NULL,
    key BIGINT NOT NULL,
    PRIMARY KEY (execution_id, key)
);

CREATE TABLE migration_worker.batch_checkpoints (
    execution_id UUID NOT NULL REFERENCES migration_worker.batch_runs(execution_id),
    sequence BIGINT NOT NULL,
    last_key BIGINT NOT NULL,
    affected_rows BIGINT NOT NULL CHECK (affected_rows > 0),
    PRIMARY KEY (execution_id, sequence)
);
COMMIT;
