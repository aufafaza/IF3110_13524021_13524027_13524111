-- Database access exists only through groups. No row means no access.
CREATE TABLE group_db_access (
    group_id    BIGINT NOT NULL REFERENCES groups(id)            ON DELETE CASCADE,
    database_id BIGINT NOT NULL REFERENCES managed_databases(id) ON DELETE CASCADE,
    level       TEXT   NOT NULL CHECK (level IN ('view', 'edit')),
    PRIMARY KEY (group_id, database_id)
);
