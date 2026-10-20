CREATE TABLE managed_databases (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT        NOT NULL UNIQUE,
    host         TEXT        NOT NULL,
    port         INTEGER     NOT NULL DEFAULT 5432,
    db_name      TEXT        NOT NULL,
    db_user      TEXT        NOT NULL,
    password_enc BYTEA       NOT NULL,
    sslmode      TEXT        NOT NULL DEFAULT 'prefer',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
