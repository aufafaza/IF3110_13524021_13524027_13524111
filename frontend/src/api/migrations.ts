import { apiFetch } from '@/api/client'

type Migration = {
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
  return apiFetch<{ migrations: Migration[] }>(`/api/databases/${encodeURIComponent(dbID)}/migrations`, { signal })
}

export function getMigration(dbID: string, migrationID: string, signal: AbortSignal) {
  const path = `/api/databases/${encodeURIComponent(dbID)}/migrations/${encodeURIComponent(migrationID)}`
  return apiFetch<Migration>(path, { signal })
}
