BEGIN;

CREATE TABLE migration_worker.batch_row_snapshots (
    execution_id UUID NOT NULL,
    key BIGINT NOT NULL,
    before_row JSONB NOT NULL,
    after_row JSONB,
    PRIMARY KEY (execution_id, key)
);

CREATE TABLE migration_worker.compensation_checkpoints (
    execution_id UUID NOT NULL,
    sequence BIGINT NOT NULL,
    recovery_execution_id UUID NOT NULL,
    attempt_id UUID NOT NULL,
    affected_rows BIGINT NOT NULL CHECK (affected_rows > 0),
    PRIMARY KEY (execution_id, sequence),
    FOREIGN KEY (execution_id, sequence) REFERENCES migration_worker.batch_checkpoints(execution_id, sequence)
);
COMMIT;
