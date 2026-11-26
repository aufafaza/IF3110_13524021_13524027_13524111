import { apiFetch } from '@/api/client'

export type Migration = {
  id: number
  title: string
  status: string
  version?: number
  sql: string
  down_sql?: string
  irreversible_reason?: string
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

export const operationLabels = { apply: 'Apply', rollback: 'Rollback', forward: 'Forward' } as const
export type MigrationOperation = keyof typeof operationLabels

export type ReviewDecision = {
  group_id: number
  group_name: string
  leader_user_id: number
  decision?: 'approved' | 'rejected'
  comment?: string
}

export type MigrationReview = {
  request_id: string
  status: 'pending' | 'approved' | 'rejected'
  target_execution_id?: string
  job_id?: number
  decisions: ReviewDecision[]
}

export type MigrationJob = {
  id: number
  state: 'queued' | 'running' | 'retrying' | 'reconciling' | 'reporting' | 'partial' | 'succeeded' | 'failed'
  attempts: number
  failure_code?: string
}

export type OperationReview = {
  review: MigrationReview | null
  plan: { sql: string; target_execution_id?: string } | null
  unavailable_reason?: string
}

function reviewPath(dbID: string, migrationID: string) {
  return `/api/databases/${encodeURIComponent(dbID)}/migrations/${encodeURIComponent(migrationID)}/review`
}

export function getOperationReview(
  dbID: string, migrationID: string, revision: number, operation: MigrationOperation, signal: AbortSignal,
) {
  const query = new URLSearchParams({ revision: String(revision), operation })
  return apiFetch<OperationReview>(`${reviewPath(dbID, migrationID)}?${query}`, { signal, cache: 'no-store' })
}

export function submitOperationReview(
  dbID: string, migrationID: string, input: { operation: MigrationOperation; revision: number; target_execution_id?: string },
  csrfToken: string, signal: AbortSignal,
) {
  return apiFetch<MigrationReview>(reviewPath(dbID, migrationID), {
    method: 'POST', csrfToken, signal,
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  })
}

export function decideOperationReview(
  requestID: string, groupID: number, decision: 'approved' | 'rejected', csrfToken: string, signal: AbortSignal,
) {
  return apiFetch<MigrationReview>(`/api/migration-reviews/${encodeURIComponent(requestID)}/decision`, {
    method: 'POST', csrfToken, signal,
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ group_id: groupID, decision }),
  })
}

export function enqueueOperationReview(requestID: string, csrfToken: string, signal: AbortSignal) {
  return apiFetch<{ id: number }>(`/api/migration-reviews/${encodeURIComponent(requestID)}/enqueue`, {
    method: 'POST', csrfToken, signal,
  })
}

export function getMigrationJob(dbID: string, jobID: number, signal: AbortSignal) {
  return apiFetch<MigrationJob>(`/api/databases/${encodeURIComponent(dbID)}/jobs/${jobID}`, {
    signal, cache: 'no-store',
  })
}

export type MigrationAttempt = {
  id: number
  migration_id: number
  revision: number
  execution_id: string
  attempt_id: string
  actor_id: number
  operation: MigrationOperation
  status: 'success' | 'failed'
  executed_sql: string
  error_message?: string
  created_at: string
}

export function listAttemptHistory(dbID: string, hideRollback: string, signal: AbortSignal) {
  const query = new URLSearchParams({ hide_rollback: hideRollback })
  return apiFetch<{ entries: MigrationAttempt[] }>(
    `/api/databases/${encodeURIComponent(dbID)}/migration-history?${query}`, { signal, cache: 'no-store' },
  )
}
