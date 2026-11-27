import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '@/api/client'
import {
  decideOperationReview, enqueueOperationReview, getOperationReview, getMigrationJob, submitOperationReview, operationLabels,
  type Migration, type MigrationJob, type MigrationOperation, type OperationReview,
} from '@/api/migrations'
import { useAuth } from '@/auth/useAuth'

const jobLabels: Record<MigrationJob['state'], string> = {
  queued: 'Queued',
  running: 'Running',
  retrying: 'Waiting to retry',
  reconciling: 'Checking the database',
  reporting: 'Saving execution history',
  partial: 'Partially applied',
  partially_reverted: 'Partially reverted',
  recovered: 'Resumed successfully',
  compensated: 'Compensated',
  succeeded: 'Succeeded',
  failed: 'Failed',
}

type OperationStatus = OperationReview & { job?: MigrationJob }

function useOperationStatus(dbID: string, migrationID: string, revision: number, operation: MigrationOperation, refresh: number) {
  const [status, setStatus] = useState<OperationStatus>()
  const [error, setError] = useState('')
  const [revisionChanged, setRevisionChanged] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    const { signal } = controller
    let timer: ReturnType<typeof setTimeout> | undefined

    async function poll() {
      let continuePolling = false
      try {
        const current = await getOperationReview(dbID, migrationID, revision, operation, signal)
        const { review } = current
        const job = review?.job_id ? await getMigrationJob(dbID, review.job_id, signal) : undefined
        if (signal.aborted) return
        setStatus({ ...current, job })
        setError('')
        setRevisionChanged(false)
        continuePolling = job
          ? !['succeeded', 'failed', 'recovered', 'compensated'].includes(job.state)
            && (!job.batch_paused || operation === 'apply')
          : (!!current.plan && review?.status === 'pending') || review?.status === 'approved'
      } catch (failure) {
        if (signal.aborted) return
        const stale = failure instanceof ApiError && failure.status === 409
        setRevisionChanged(stale)
        setError(stale ? 'The draft changed. Reload the migration to review the current SQL.'
          : failure instanceof ApiError ? failure.message : 'Could not refresh execution status.')
        continuePolling = !(failure instanceof ApiError) || failure.status >= 500
      }

      if (continuePolling) timer = setTimeout(() => void poll(), 1000)
    }

    void poll()
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [dbID, migrationID, revision, operation, refresh])

  return { status, error, revisionChanged }
}

function workflowLabel(migration: Migration, operation: MigrationOperation, status: OperationStatus | undefined) {
  if (status?.job) return jobLabels[status.job.state]
  switch (status?.review?.status) {
    case 'pending': return 'Pending review'
    case 'approved': return 'Approved'
    case 'rejected': return 'Rejected'
    default:
      if (!status) return 'Loading…'
      if (operation !== 'apply') return status.plan ? 'Ready for review' : 'Unavailable'
      if (migration.status === 'rolled_back') return 'Rolled back'
      if (migration.status === 'applied') return 'Applied'
      return migration.status === 'failed' ? 'Failed' : 'Draft'
  }
}

type WorkflowProps = {
  dbID: string
  migrationID: string
  migration: Migration & { can_edit: boolean }
}

export function MigrationWorkflow(props: WorkflowProps) {
  const [operation, setOperation] = useState<MigrationOperation>('apply')
  const operations: MigrationOperation[] = props.migration.batch_config
    ? ['apply', 'batch_recovery', 'compensation']
    : ['apply', 'rollback', 'forward']
  return (
    <section aria-labelledby="migration-workflow">
      <h2 id="migration-workflow">Approval and execution</h2>
      <div className="action-row" role="group" aria-label="Migration operation">
        {operations.map((choice) => (
          <button key={choice} type="button" aria-pressed={operation === choice}
            className={operation === choice ? undefined : 'button-secondary'} onClick={() => setOperation(choice)}>
            {operationLabels[choice]}
          </button>
        ))}
      </div>
      <MigrationOperationPanel key={operation} {...props} operation={operation} />
    </section>
  )
}

function MigrationOperationPanel({ dbID, migrationID, migration, operation }: WorkflowProps & { operation: MigrationOperation }) {
  const { state } = useAuth()
  const session = state.status === 'ready' && state.session.authenticated ? state.session : undefined
  const [refresh, setRefresh] = useState(0)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const mutation = useRef<AbortController | undefined>(undefined)
  const { status, error, revisionChanged } = useOperationStatus(dbID, migrationID, migration.revision, operation, refresh)

  useEffect(() => () => mutation.current?.abort(), [])

  async function act(action: (csrfToken: string, signal: AbortSignal) => Promise<unknown>) {
    if (!session || mutation.current) return
    const controller = new AbortController()
    mutation.current = controller
    setBusy(true)
    setActionError('')
    try {
      await action(session.csrf_token, controller.signal)
    } catch (failure) {
      if (!controller.signal.aborted) {
        setActionError(failure instanceof ApiError ? failure.message : 'Could not update the migration.')
      }
    } finally {
      mutation.current = undefined
      if (!controller.signal.aborted) {
        setBusy(false)
        setRefresh((count) => count + 1)
      }
    }
  }

  const review = status?.review
  const plan = status?.plan
  const job = status?.job
  const canRequestAgain = operation !== 'apply' && (job?.state === 'failed' || job?.batch_paused)
  const operationLabel = operationLabels[operation].toLowerCase()
  const unsafeCompensation = operation === 'compensation' &&
    (job?.failure_code === 'compensation_unsafe' || status?.unavailable_reason?.includes('create a corrective migration'))
  const canEditDraft = operation === 'apply' && !revisionChanged && migration.can_edit && migration.status === 'draft' && !migration.first_executed_at
    && (!review || review.status !== 'approved')

  return (
    <>
      {(operation === 'rollback' || operation === 'compensation') && (plan?.sql || migration.down_sql) && (
        <>
          <h3>Down SQL</h3>
          <pre className="sql-view" tabIndex={0}>{plan?.sql ?? migration.down_sql}</pre>
        </>
      )}
      {operation === 'apply' && migration.batch_config && (
        <details open={!!review && review.status === 'pending'}>
          <summary>Batch scope and size</summary>
          <p>{migration.batch_config.size} rows per batch</p>
          {migration.batch_config.recovery && (
            <p>Compensation snapshots use {migration.batch_config.recovery.schema}.{migration.batch_config.recovery.table}
              {' · key '}{migration.batch_config.recovery.key}</p>
          )}
          <pre className="sql-view" tabIndex={0}>{migration.batch_config.scope_sql}</pre>
        </details>
      )}
      <dl className="migration-summary" aria-live="polite">
        <dt>Status</dt><dd>{workflowLabel(migration, operation, status)}</dd>
        {job && <><dt>Job</dt><dd>#{job.id}</dd><dt>Attempts</dt><dd>{job.attempts}</dd></>}
        {job?.mode === 'batch' && (
          <>
            <dt>Committed</dt><dd>{job.committed_batches} batches · {job.committed_rows}{job.scope_rows !== undefined ? ` of ${job.scope_rows}` : ''} rows</dd>
            {(job.compensated_batches > 0 || operation === 'compensation') && (
              <><dt>Compensated</dt><dd>{job.compensated_batches} batches · {job.compensated_rows} rows</dd></>
            )}
          </>
        )}
      </dl>
      {job?.failure_code && !unsafeCompensation && (
        <p className="feedback">
          {job.failure_code === 'ineligible'
            ? 'The approval, editing permissions, or execution order changed before execution.'
            : `Execution detail: ${job.failure_code.replaceAll('_', ' ')}`}
        </p>
      )}
      {error && (
        <div className="action-row">
          <p className="feedback feedback-error" role="alert">{error}</p>
          {revisionChanged
            ? <button type="button" onClick={() => window.location.reload()}>Reload migration</button>
            : <button type="button" onClick={() => setRefresh((count) => count + 1)}>Refresh status</button>}
        </div>
      )}
      {!unsafeCompensation && !job && !plan && status?.unavailable_reason && operation !== 'apply' && (
        <p className="feedback">{status.unavailable_reason.replace(/^migration: /, '')}</p>
      )}
      {unsafeCompensation && (
        <p className="feedback feedback-error" role="alert">
          Safe compensation is unavailable. {migration.can_edit && (
            <Link to={`/databases/${dbID}/migrations/new`}>Create a corrective migration for review.</Link>
          )}
        </p>
      )}
      {actionError && <p className="feedback feedback-error" role="alert">{actionError}</p>}
      {!!review?.decisions.length && (
        <ul className="resource-list" aria-label="Required group decisions">
          {review.decisions.map((group) => (
            <li className="resource-row review-group" key={group.group_id}>
              <div>
                <strong>{group.group_name}</strong>
                <span className="account-name"> · {group.decision ?? 'Pending'}</span>
                {group.comment && <div>{group.comment}</div>}
              </div>
              {plan && !revisionChanged && review.status === 'pending' && !group.decision && session?.user.id === group.leader_user_id && (
                <div className="action-row">
                  <button type="button" disabled={busy} aria-label={`Approve for ${group.group_name}`}
                    onClick={() => void act((token, signal) => decideOperationReview(review.request_id, group.group_id, 'approved', token, signal))}>
                    Approve
                  </button>
                  <button type="button" disabled={busy} aria-label={`Reject for ${group.group_name}`}
                    onClick={() => void act((token, signal) => decideOperationReview(review.request_id, group.group_id, 'rejected', token, signal))}>
                    Reject
                  </button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {status && (
        <div className="action-row">
          {canEditDraft && <Link to={`/databases/${dbID}/migrations/${migrationID}/edit`}>Edit draft</Link>}
          {!revisionChanged && migration.can_edit && plan && (!review || canRequestAgain) && !unsafeCompensation && (
            <button type="button" disabled={busy}
              onClick={() => void act((token, signal) => submitOperationReview(dbID, migrationID, {
                operation, revision: migration.revision, target_execution_id: plan.target_execution_id,
              }, token, signal))}>
              {operation === 'apply' ? 'Submit for approval' : canRequestAgain
                ? `Request another review to ${operationLabel}` : `Request approval to ${operationLabel}`}
            </button>
          )}
          {!revisionChanged && migration.can_edit && plan && review?.status === 'approved' && !review.job_id && (
            <button type="button" disabled={busy}
              onClick={() => void act((token, signal) => enqueueOperationReview(review.request_id, token, signal))}>
              Queue migration
            </button>
          )}
        </div>
      )}
    </>
  )
}
