-- Reflect successful async applies on the existing migration list/detail API.
-- Failed attempts stay in attempt history while the job handles retries.
CREATE FUNCTION migration_history_project_apply() RETURNS trigger AS $$
BEGIN
    PERFORM id FROM managed_databases WHERE id = NEW.database_id FOR NO KEY UPDATE;

    UPDATE migrations m
       SET status = 'applied',
           version = COALESCE(m.version, (
               SELECT COALESCE(MAX(other.version), 0) + 1
               FROM migrations other WHERE other.database_id = m.database_id
           )),
           executed_by = NEW.actor_id,
           executed_at = NEW.created_at,
           error_message = NULL
     WHERE m.database_id = NEW.database_id AND m.id = NEW.migration_id
       AND m.revision = NEW.revision AND m.status = 'draft';

    RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_history_project_apply
    AFTER INSERT ON migration_history FOR EACH ROW
    WHEN (NEW.operation = 'apply' AND NEW.status = 'success')
    EXECUTE FUNCTION migration_history_project_apply();

-- Project jobs that completed before this migration, in their recorded order.
WITH first_apply AS (
    SELECT DISTINCT ON (h.migration_id) h.id, h.migration_id, h.database_id, h.actor_id, h.created_at
    FROM migration_history h
    JOIN migrations m
      ON m.id = h.migration_id AND m.database_id = h.database_id AND m.revision = h.revision
    WHERE h.operation = 'apply' AND h.status = 'success' AND m.status = 'draft'
    ORDER BY h.migration_id, h.id
), numbered AS (
    SELECT first_apply.*, ROW_NUMBER() OVER (PARTITION BY database_id ORDER BY id) AS version_offset
    FROM first_apply
), last_version AS (
    SELECT database_id, COALESCE(MAX(version), 0) AS version
    FROM migrations GROUP BY database_id
)
UPDATE migrations m
   SET status = 'applied',
       version = COALESCE(m.version, last_version.version + numbered.version_offset),
       executed_by = numbered.actor_id,
       executed_at = numbered.created_at,
       error_message = NULL
  FROM numbered JOIN last_version USING (database_id)
 WHERE m.id = numbered.migration_id;
