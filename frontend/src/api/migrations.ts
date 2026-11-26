import { apiFetch } from '@/api/client'

export type Migration = {
  id: number
  title: string
  status: string
  version?: number
  sql: string
  created_at: string
  executed_at?: string
  first_executed_at?: string
  revision: number
  error_message?: string
}

export function listMigrations(dbID: string, signal: AbortSignal) {
  return apiFetch<{ migrations: Migration[]; can_edit: boolean }>(`/api/databases/${encodeURIComponent(dbID)}/migrations`, { signal })
}

export function getMigration(dbID: string, migrationID: string, signal: AbortSignal) {
  const path = `/api/databases/${encodeURIComponent(dbID)}/migrations/${encodeURIComponent(migrationID)}`
  return apiFetch<Migration & { can_edit: boolean }>(path, { signal })
}

export type MigrationInput = { title: string; sql: string }
export type Preview = {
  statement_count: number
  statements: string[]
  plan_report?: { statements: { index: number; is_explainable: boolean; raw_plan: string }[] }
}

export function migrationPost<T>(
  dbID: string, migrationID: string | undefined, csrfToken: string, body: object,
) {
  const root = `/api/databases/${encodeURIComponent(dbID)}/migrations`
  const path = migrationID ? `${root}/${encodeURIComponent(migrationID)}` : root
  return apiFetch<T>(path, {
    method: 'POST', csrfToken, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })
}

export type ReviewDecision = {
  group_id: number
  group_name: string
  leader_user_id: number
  decision?: 'approved' | 'rejected'
  comment?: string
}

export type ApplyReview = {
  request_id: string
  status: 'pending' | 'approved' | 'rejected'
  job_id?: number
  decisions: ReviewDecision[]
}

export type MigrationJob = {
  id: number
  state: 'queued' | 'running' | 'retrying' | 'reconciling' | 'reporting' | 'partial' | 'succeeded' | 'failed'
  attempts: number
  failure_code?: string
}

function applyReviewPath(dbID: string, migrationID: string) {
  return `/api/databases/${encodeURIComponent(dbID)}/migrations/${encodeURIComponent(migrationID)}/review`
}

export function getApplyReview(dbID: string, migrationID: string, revision: number, signal: AbortSignal) {
  return apiFetch<{ review: ApplyReview | null }>(`${applyReviewPath(dbID, migrationID)}?revision=${revision}`, { signal, cache: 'no-store' })
}

export function submitApplyReview(dbID: string, migrationID: string, revision: number, csrfToken: string, signal: AbortSignal) {
  return apiFetch<ApplyReview>(applyReviewPath(dbID, migrationID), {
    method: 'POST', csrfToken, signal,
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ operation: 'apply', revision }),
  })
}

export function decideApplyReview(
  requestID: string, groupID: number, decision: 'approved' | 'rejected', csrfToken: string, signal: AbortSignal,
) {
  return apiFetch<ApplyReview>(`/api/migration-reviews/${encodeURIComponent(requestID)}/decision`, {
    method: 'POST', csrfToken, signal,
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ group_id: groupID, decision }),
  })
}

export function enqueueApplyReview(requestID: string, csrfToken: string, signal: AbortSignal) {
  return apiFetch<{ id: number }>(`/api/migration-reviews/${encodeURIComponent(requestID)}/enqueue`, {
    method: 'POST', csrfToken, signal,
  })
}

export function getMigrationJob(dbID: string, jobID: number, signal: AbortSignal) {
  return apiFetch<MigrationJob>(`/api/databases/${encodeURIComponent(dbID)}/jobs/${jobID}`, {
    signal, cache: 'no-store',
  })
}
