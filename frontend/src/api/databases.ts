import { apiFetch } from '@/api/client'

export type Database = { id: number; name: string }
export type DatabaseTables = {
  database: Database
  tables: { schema: string; name: string }[]
}

export function listDatabases(signal: AbortSignal) {
  return apiFetch<{ databases: Database[] }>('/api/databases', { signal })
}

export function listTables(dbID: string, signal: AbortSignal) {
  return apiFetch<DatabaseTables>(`/api/databases/${encodeURIComponent(dbID)}/tables`, { signal })
}
