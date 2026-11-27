ALTER TABLE migrations ADD COLUMN batch_config JSONB;

CREATE FUNCTION migrations_guard_batch_config() RETURNS trigger AS $$
BEGIN
    IF OLD.first_executed_at IS NOT NULL AND NEW.batch_config IS DISTINCT FROM OLD.batch_config THEN
        RAISE EXCEPTION 'migration % batch configuration is immutable', OLD.id USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_migrations_batch_immutable BEFORE UPDATE ON migrations
    FOR EACH ROW EXECUTE FUNCTION migrations_guard_batch_config();

ALTER TABLE migration_review_requests ADD COLUMN mode TEXT NOT NULL DEFAULT 'ordinary'
    CHECK (mode IN ('ordinary', 'batch'));

DROP INDEX migration_review_requests_pending_dispatch;
CREATE INDEX migration_review_requests_pending_dispatch ON migration_review_requests (decided_at, request_id)
    WHERE status = 'approved' AND async_job_id IS NULL AND superseded_at IS NULL;

ALTER TABLE migration_async_jobs
    DROP CONSTRAINT migration_async_jobs_operation_check,
    DROP CONSTRAINT migration_async_jobs_state_check,
    ADD CONSTRAINT migration_async_jobs_operation_check CHECK (
        (operation = 'apply' AND target_execution_id = '')
        OR (operation IN ('rollback', 'forward') AND mode = 'ordinary' AND target_execution_id <> '')
        OR (operation IN ('batch_recovery', 'compensation') AND mode = 'batch' AND target_execution_id <> '')
    ),
    ADD CONSTRAINT migration_async_jobs_state_check CHECK (state IN (
        'queued', 'running', 'reconciling', 'reporting', 'retrying', 'partial',
        'partially_reverted', 'succeeded', 'failed', 'recovered', 'compensated'
    )),
    ADD COLUMN compensated_batches BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN compensated_rows BIGINT NOT NULL DEFAULT 0;

CREATE INDEX migration_async_jobs_recovery_target
    ON migration_async_jobs (database_id, target_execution_id, id)
    WHERE operation IN ('batch_recovery', 'compensation');

ALTER TABLE migration_history
    DROP CONSTRAINT migration_history_operation_check,
    ADD CONSTRAINT migration_history_operation_check CHECK (operation IN (
        'apply', 'rollback', 'forward', 'batch_recovery', 'compensation'
    )),
    ADD COLUMN recovery_of_execution_id TEXT,
    ADD CONSTRAINT migration_history_recovery_target_check CHECK (
        (operation IN ('batch_recovery', 'compensation')) = (recovery_of_execution_id IS NOT NULL)
    );

DROP TRIGGER trg_history_project_apply ON migration_history;
CREATE TRIGGER trg_history_project_apply AFTER INSERT ON migration_history FOR EACH ROW
    WHEN (NEW.operation IN ('apply', 'batch_recovery') AND NEW.status = 'success')
    EXECUTE FUNCTION migration_history_project_apply();
