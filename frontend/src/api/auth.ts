import { apiFetch } from '@/api/client'

export type SessionUser = {
  id: number
  username: string
  is_platform_admin: boolean
}

export type Session = (
  | { authenticated: true; user: SessionUser }
  | { authenticated: false; user: null }
) & { csrf_token: string }

export function getSession() {
  return apiFetch<Session>('/api/auth/session')
}

export function postLogin(username: string, password: string, csrfToken: string) {
  return apiFetch<{ user: SessionUser }>('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
    csrfToken,
  })
}

export function postLogout(csrfToken: string) {
  return apiFetch<{ authenticated: false }>('/api/auth/logout', {
    method: 'POST',
    csrfToken,
  })
}
