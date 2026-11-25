import { apiFetch } from '@/api/client'

export type Database = { id: number; name: string }
export type DatabaseTables = {
  database: Database
  tables: { schema: string; name: string }[]
}

export type TableInfo = {
  columns: {
    name: string
    type: string
    nullable: boolean
    default: string | null
  }[]
  constraints: {
    name: string
    type: string
    definition: string
  }[]
  indexes: {
    name: string
    unique: boolean
    method: string
    definition: string
  }[]
}

export function listDatabases(signal: AbortSignal) {
  return apiFetch<{ databases: Database[] }>('/api/databases', { signal })
}

export function listTables(dbID: string, signal: AbortSignal) {
  return apiFetch<DatabaseTables>(`/api/databases/${encodeURIComponent(dbID)}/tables`, { signal })
}

export function getTableInfo(
  dbID: string,
  schema: string,
  table: string,
  signal: AbortSignal,
) {
  const query = new URLSearchParams({ schema, table })
  return apiFetch<TableInfo>(`/api/databases/${encodeURIComponent(dbID)}/table?${query}`, { signal })
}

export type TableRows = {
  columns: string[]
  rows: { text: string; null: boolean }[][]
  page: number
  has_next: boolean
}

export function getTableRows(dbID: string, schema: string, table: string, page: number, signal: AbortSignal) {
  const query = new URLSearchParams({ schema, table, page: String(page) })
  return apiFetch<TableRows>(`/api/databases/${encodeURIComponent(dbID)}/table/rows?${query}`, { signal })
}
