ALTER TABLE groups ADD COLUMN IF NOT EXISTS leader_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS migration_review_requests (
    request_id    TEXT PRIMARY KEY,
    migration_id  BIGINT NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    revision      TEXT NOT NULL,
    operation     TEXT NOT NULL CHECK (operation IN ('apply', 'rollback', 'forward', 'batch_recovery', 'compensation')),
    requested_by  BIGINT NOT NULL REFERENCES users(id),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at    TIMESTAMPTZ,
    enqueued_at   TIMESTAMPTZ,
    UNIQUE (migration_id, revision, operation)
);

CREATE TABLE IF NOT EXISTS migration_review_decisions (
    request_id     TEXT NOT NULL REFERENCES migration_review_requests(request_id) ON DELETE CASCADE,
    group_id       BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    leader_user_id BIGINT NOT NULL REFERENCES users(id),
    decision       TEXT CHECK (decision IN ('approved', 'rejected')),
    comment        TEXT NOT NULL DEFAULT '',
    decided_at     TIMESTAMPTZ,
    PRIMARY KEY (request_id, group_id)
);

CREATE TABLE IF NOT EXISTS migration_queue_jobs (
    id          BIGSERIAL PRIMARY KEY,
    request_id  TEXT NOT NULL UNIQUE REFERENCES migration_review_requests(request_id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'success', 'failed')),
    attempts    INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
