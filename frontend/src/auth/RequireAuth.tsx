import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '@/auth/useAuth'

export function RequireAuth() {
  const { state, refreshSession } = useAuth()

  if (state.status === 'loading') return <p role="status">Loading session…</p>
  if (state.status === 'error') {
    return (
      <>
        <p role="alert">{state.message}</p>
        <button type="button" onClick={() => void refreshSession()}>Retry</button>
      </>
    )
  }

  return state.session.authenticated ? <Outlet /> : <Navigate to="/login" replace />
}
