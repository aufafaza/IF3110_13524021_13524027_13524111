ALTER TABLE migrations
    ADD COLUMN down_sql            TEXT,
    ADD COLUMN irreversible_reason TEXT,
    ADD COLUMN revision            INTEGER     NOT NULL DEFAULT 1,
    ADD COLUMN first_executed_at   TIMESTAMPTZ;

UPDATE migrations
   SET first_executed_at   = executed_at
 WHERE status IN ('applied', 'failed');
UPDATE migrations
   SET irreversible_reason = 'No down SQL defined'
 WHERE irreversible_reason IS NULL AND down_sql IS NULL;

ALTER TABLE migrations
    ADD CONSTRAINT migrations_reversibility_chk
        CHECK (down_sql IS NOT NULL OR irreversible_reason IS NOT NULL),
    ADD CONSTRAINT migrations_not_both_chk
        CHECK (NOT (down_sql IS NOT NULL AND irreversible_reason IS NOT NULL));

CREATE FUNCTION migrations_guard_immutable() RETURNS trigger AS $$
BEGIN
    IF OLD.first_executed_at IS NOT NULL AND (
           NEW.sql                IS DISTINCT FROM OLD.sql
        OR NEW.down_sql           IS DISTINCT FROM OLD.down_sql
        OR NEW.irreversible_reason IS DISTINCT FROM OLD.irreversible_reason
        OR NEW.revision           IS DISTINCT FROM OLD.revision) THEN
        RAISE EXCEPTION 'migration % definition is immutable after first execution', OLD.id
            USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_migrations_immutable
    BEFORE UPDATE ON migrations
    FOR EACH ROW EXECUTE FUNCTION migrations_guard_immutable();

CREATE TABLE migration_history (
    id                        BIGSERIAL PRIMARY KEY,
    database_id               BIGINT      NOT NULL REFERENCES managed_databases(id) ON DELETE CASCADE,
    migration_id              BIGINT      NOT NULL REFERENCES migrations(id),
    revision                  INTEGER     NOT NULL,
    execution_id              TEXT        NOT NULL,
    attempt_id                TEXT        NOT NULL,
    operation                 TEXT        NOT NULL CHECK (operation IN ('apply', 'rollback', 'forward')),
    status                    TEXT        NOT NULL CHECK (status IN ('success', 'failed')),
    actor_id                  BIGINT      NOT NULL REFERENCES users(id),
    executed_sql              TEXT        NOT NULL,
    error_message             TEXT,
    rollback_of_execution_id  TEXT,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (execution_id, attempt_id),
    CHECK ((operation = 'rollback' AND status = 'success') = (rollback_of_execution_id IS NOT NULL))
);
CREATE INDEX migration_history_db_idx ON migration_history (database_id, id);

INSERT INTO migration_history
    (database_id, migration_id, revision, execution_id, attempt_id, operation,
     status, actor_id, executed_sql, error_message, created_at)
SELECT database_id, id, 1, 'exec-' || id, 'a1', 'apply',
       CASE status WHEN 'applied' THEN 'success' ELSE 'failed' END,
       COALESCE(executed_by, created_by), sql, error_message,
       COALESCE(executed_at, created_at)
  FROM migrations
 WHERE status IN ('applied', 'failed');

CREATE FUNCTION migration_history_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'migration_history is append-only' USING ERRCODE = '23000';
    END IF;
    UPDATE migrations SET first_executed_at = now()
     WHERE id = NEW.migration_id AND first_executed_at IS NULL;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_history_no_update BEFORE UPDATE ON migration_history
    FOR EACH ROW EXECUTE FUNCTION migration_history_guard();
CREATE TRIGGER trg_history_mark_executed AFTER INSERT ON migration_history
    FOR EACH ROW EXECUTE FUNCTION migration_history_guard();