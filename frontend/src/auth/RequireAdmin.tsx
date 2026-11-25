import { Outlet } from 'react-router-dom'
import { useAuth } from '@/auth/useAuth'

export function RequireAdmin() {
  const { state } = useAuth()
  if (state.status === 'ready' && state.session.user?.is_platform_admin) return <Outlet />
  return <h1>Administrator access required</h1>
}
