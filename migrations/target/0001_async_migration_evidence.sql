BEGIN;
CREATE SCHEMA migration_worker;
REVOKE ALL ON SCHEMA migration_worker FROM PUBLIC;

CREATE TABLE migration_worker.target_identity (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    id UUID NOT NULL DEFAULT gen_random_uuid()
);
INSERT INTO migration_worker.target_identity DEFAULT VALUES;

CREATE TABLE migration_worker.completions (
    execution_id UUID PRIMARY KEY,
    attempt_id UUID NOT NULL,
    plan_hash TEXT NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
COMMIT;
