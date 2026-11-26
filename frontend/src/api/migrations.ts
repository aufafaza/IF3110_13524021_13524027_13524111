import { apiFetch } from '@/api/client'

export type Migration = {
  id: number
  title: string
  status: string
  version?: number
  sql: string
  created_at: string
  executed_at?: string
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
  dbID: string, migrationID: string | undefined, csrfToken: string, body: object, execute = false,
) {
  const root = `/api/databases/${encodeURIComponent(dbID)}/migrations`
  const path = migrationID ? `${root}/${encodeURIComponent(migrationID)}` : root
  return apiFetch<T>(execute ? `${path}/execute` : path, {
    method: 'POST', csrfToken, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })
}
