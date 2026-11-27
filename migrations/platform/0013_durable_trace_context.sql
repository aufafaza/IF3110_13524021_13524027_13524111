ALTER TABLE migration_review_requests
    ADD COLUMN traceparent TEXT NOT NULL DEFAULT '',
    ADD COLUMN tracestate TEXT NOT NULL DEFAULT '',
    ADD COLUMN correlation_id TEXT NOT NULL DEFAULT '';

ALTER TABLE migration_async_jobs
    ADD COLUMN correlation_id TEXT NOT NULL DEFAULT '';
