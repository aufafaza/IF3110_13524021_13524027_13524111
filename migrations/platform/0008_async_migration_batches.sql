ALTER TABLE migration_async_jobs
    DROP CONSTRAINT migration_async_jobs_state_check,
    ADD CONSTRAINT migration_async_jobs_state_check
        CHECK (state IN ('queued', 'running', 'reconciling', 'reporting', 'retrying', 'partial', 'succeeded', 'failed')),
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'ordinary' CHECK (mode IN ('ordinary', 'batch')),
    ADD COLUMN scope_rows BIGINT,
    ADD COLUMN committed_batches BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN committed_rows BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN batch_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN batch_pending BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN batch_paused BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE migration_async_attempts ADD COLUMN batch_plan JSONB;
