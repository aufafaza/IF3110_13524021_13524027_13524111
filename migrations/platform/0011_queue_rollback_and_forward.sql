ALTER TABLE migration_review_requests
    DROP CONSTRAINT migration_review_requests_migration_id_revision_operation_key,
    ADD COLUMN target_execution_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN superseded_at TIMESTAMPTZ;

CREATE UNIQUE INDEX migration_review_requests_current_operation
    ON migration_review_requests (migration_id, revision, operation, target_execution_id)
    WHERE superseded_at IS NULL;

DROP INDEX migration_review_requests_pending_dispatch;
CREATE INDEX migration_review_requests_pending_dispatch
    ON migration_review_requests (decided_at, request_id)
    WHERE status = 'approved' AND async_job_id IS NULL AND superseded_at IS NULL
      AND (operation = 'apply' OR (operation IN ('rollback', 'forward') AND target_execution_id <> ''));

ALTER TABLE migration_async_jobs
    DROP CONSTRAINT migration_async_jobs_operation_check,
    ADD COLUMN target_execution_id TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT migration_async_jobs_operation_check CHECK (
        (operation = 'apply' AND target_execution_id = '')
        OR (operation IN ('rollback', 'forward') AND mode = 'ordinary' AND target_execution_id <> '')
    );

ALTER TABLE migration_async_attempts ADD COLUMN rollback_of_execution_id TEXT;

ALTER TABLE migrations
    DROP CONSTRAINT migrations_status_check,
    ADD CONSTRAINT migrations_status_check CHECK (status IN ('draft', 'applied', 'failed', 'rolled_back'));

CREATE FUNCTION migration_history_project_direction() RETURNS trigger AS $$
BEGIN
    UPDATE migrations SET
        status = CASE WHEN NEW.operation = 'rollback' THEN 'rolled_back' ELSE 'applied' END,
        executed_by = NEW.actor_id,
        executed_at = NEW.created_at,
        error_message = NULL
    WHERE id = NEW.migration_id AND database_id = NEW.database_id AND revision = NEW.revision;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_history_project_direction
    AFTER INSERT ON migration_history FOR EACH ROW
    WHEN (NEW.operation IN ('rollback', 'forward') AND NEW.status = 'success')
    EXECUTE FUNCTION migration_history_project_direction();
