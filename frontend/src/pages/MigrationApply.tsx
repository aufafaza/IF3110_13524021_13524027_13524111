import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '@/api/client'
import {
  decideApplyReview, enqueueApplyReview, getApplyReview, getMigrationJob, submitApplyReview,
  type ApplyReview, type Migration, type MigrationJob,
} from '@/api/migrations'
import { useAuth } from '@/auth/useAuth'

const jobLabels: Record<MigrationJob['state'], string> = {
  queued: 'Queued',
  running: 'Running',
  retrying: 'Waiting to retry',
  reconciling: 'Checking the database',
  reporting: 'Saving execution history',
  partial: 'Partially applied',
  succeeded: 'Succeeded',
  failed: 'Failed',
}

type ApplyStatus = { review: ApplyReview | null; job?: MigrationJob }

function useApplyStatus(dbID: string, migrationID: string, revision: number, refresh: number) {
  const [status, setStatus] = useState<ApplyStatus>()
  const [error, setError] = useState('')
  const [revisionChanged, setRevisionChanged] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    const { signal } = controller
    let timer: ReturnType<typeof setTimeout> | undefined

    async function poll() {
      let continuePolling = false
      try {
        const { review } = await getApplyReview(dbID, migrationID, revision, signal)
        const job = review?.job_id ? await getMigrationJob(dbID, review.job_id, signal) : undefined
        if (signal.aborted) return
        setStatus({ review, job })
        setError('')
        setRevisionChanged(false)
        continuePolling = job
          ? job.state !== 'succeeded' && job.state !== 'failed'
          : review?.status === 'pending' || review?.status === 'approved'
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
  }, [dbID, migrationID, revision, refresh])

  return { status, error, revisionChanged }
}

function workflowLabel(migration: Migration, status: ApplyStatus | undefined) {
  if (status?.job) return jobLabels[status.job.state]
  switch (status?.review?.status) {
    case 'pending': return 'Pending review'
    case 'approved': return 'Approved'
    case 'rejected': return 'Rejected'
    default:
      if (!status) return 'Loading…'
      if (migration.status === 'applied') return 'Applied'
      return migration.status === 'failed' ? 'Failed' : 'Draft'
  }
}

export function MigrationApply({ dbID, migrationID, migration }: {
  dbID: string
  migrationID: string
  migration: Migration & { can_edit: boolean }
}) {
  const { state } = useAuth()
  const session = state.status === 'ready' && state.session.authenticated ? state.session : undefined
  const [refresh, setRefresh] = useState(0)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const mutation = useRef<AbortController | undefined>(undefined)
  const { status, error, revisionChanged } = useApplyStatus(dbID, migrationID, migration.revision, refresh)

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
  const job = status?.job
  const canEditDraft = !revisionChanged && migration.can_edit && migration.status === 'draft' && !migration.first_executed_at
    && (!review || review.status !== 'approved')

  return (
    <section aria-labelledby="apply-heading">
      <h2 id="apply-heading">Approval and execution</h2>
      <dl className="migration-summary" aria-live="polite">
        <dt>Status</dt><dd>{workflowLabel(migration, status)}</dd>
        {job && <><dt>Job</dt><dd>#{job.id}</dd><dt>Attempts</dt><dd>{job.attempts}</dd></>}
      </dl>
      {job?.failure_code && (
        <p className="feedback">
          {job.failure_code === 'ineligible'
            ? 'The approval or editing permissions changed before execution.'
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
              {!revisionChanged && review.status === 'pending' && !group.decision && session?.user.id === group.leader_user_id && (
                <div className="action-row">
                  <button type="button" disabled={busy} aria-label={`Approve for ${group.group_name}`}
                    onClick={() => void act((token, signal) => decideApplyReview(review.request_id, group.group_id, 'approved', token, signal))}>
                    Approve
                  </button>
                  <button type="button" disabled={busy} aria-label={`Reject for ${group.group_name}`}
                    onClick={() => void act((token, signal) => decideApplyReview(review.request_id, group.group_id, 'rejected', token, signal))}>
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
          {canEditDraft && !review && (
            <button type="button" disabled={busy}
              onClick={() => void act((token, signal) => submitApplyReview(dbID, migrationID, migration.revision, token, signal))}>
              Submit for approval
            </button>
          )}
          {!revisionChanged && migration.can_edit && review?.status === 'approved' && !review.job_id && (
            <button type="button" disabled={busy}
              onClick={() => void act((token, signal) => enqueueApplyReview(review.request_id, token, signal))}>
              Queue migration
            </button>
          )}
        </div>
      )}
    </section>
  )
}
