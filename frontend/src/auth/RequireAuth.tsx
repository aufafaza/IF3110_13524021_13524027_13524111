import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '@/auth/useAuth'

export function RequireAuth() {
  const { state, refreshSession } = useAuth()

  if (state.status === 'loading') {
    return <main className="page"><p className="feedback" role="status">Loading session…</p></main>
  }
  if (state.status === 'error') {
    return (
      <main className="page">
        <p className="feedback feedback-error" role="alert">{state.message}</p>
        <button className="button-secondary" type="button" onClick={() => void refreshSession()}>Retry</button>
      </main>
    )
  }

  return state.session.authenticated ? <Outlet /> : <Navigate to="/login" replace />
}
