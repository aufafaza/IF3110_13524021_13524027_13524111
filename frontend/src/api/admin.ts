import { apiFetch } from '@/api/client'

type User = { id: number; username: string; is_active: boolean; is_platform_admin: boolean }
type Group = { id: number; name: string; member_count: number }
type Grant = { database_id: number; database_name: string; level: number }
export type ManagedDatabase = {
  id: number
  name: string
  host: string
  port: number
  db_name: string
  db_user: string
  sslmode: string
}

export function adminUsers(_revision: number, signal: AbortSignal) {
  return apiFetch<{ users: User[] }>('/api/users', { signal })
}

export function adminGroups(_revision: number, signal: AbortSignal) {
  return apiFetch<{ groups: Group[] }>('/api/groups', { signal })
}

export function adminGroup(id: string, _revision: number, signal: AbortSignal) {
  return apiFetch<{ group: Group; members: User[] | null; grants: Grant[] | null }>(`/api/groups/${id}`, { signal })
}

export function adminDatabases(_revision: number, signal: AbortSignal) {
  return apiFetch<{ databases: ManagedDatabase[] }>('/api/admin/databases', { signal })
}

export function adminDatabase(id: string, signal: AbortSignal) {
  return apiFetch<ManagedDatabase>(`/api/admin/databases/${id}`, { signal })
}
