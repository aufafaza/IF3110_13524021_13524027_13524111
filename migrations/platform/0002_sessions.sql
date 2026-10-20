-- Only hashes of session IDs are stored, never the raw value.
CREATE TABLE sessions (
    id_hash             BYTEA       PRIMARY KEY,
    prev_id_hash        BYTEA,
    prev_valid_until    TIMESTAMPTZ,
    user_id             BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_secret         BYTEA       NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ
);

CREATE INDEX sessions_prev_id_hash_idx ON sessions (prev_id_hash) WHERE prev_id_hash IS NOT NULL;
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
