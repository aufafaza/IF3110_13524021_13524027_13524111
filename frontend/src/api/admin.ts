import { apiFetch } from '@/api/client'

type User = { ID: number; Username: string; IsActive: boolean; IsPlatformAdmin: boolean }
type Group = { ID: number; Name: string; MemberCount: number }
type Grant = { DatabaseID: number; DatabaseName: string; Level: number }
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
