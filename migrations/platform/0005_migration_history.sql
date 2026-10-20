-- version is NULL while the migration is a draft; it is assigned at execution.
CREATE TABLE migrations (
    id            BIGSERIAL PRIMARY KEY,
    database_id   BIGINT      NOT NULL REFERENCES managed_databases(id) ON DELETE CASCADE,
    version       INTEGER,
    title         TEXT        NOT NULL,
    sql           TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'applied', 'failed')),
    created_by    BIGINT      NOT NULL REFERENCES users(id),
    executed_by   BIGINT      REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    executed_at   TIMESTAMPTZ,
    error_message TEXT,
    UNIQUE (database_id, version)
);
