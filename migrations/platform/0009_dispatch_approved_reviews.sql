ALTER TABLE migration_review_requests
    ADD COLUMN async_job_id BIGINT REFERENCES migration_async_jobs(id) ON DELETE SET NULL;

CREATE INDEX migration_review_requests_pending_dispatch
    ON migration_review_requests (decided_at, request_id)
    WHERE status = 'approved' AND operation = 'apply' AND async_job_id IS NULL;
