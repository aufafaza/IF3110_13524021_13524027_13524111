import { useState } from 'react'
import { Link, Outlet } from 'react-router-dom'
import { useAuth } from '@/auth/useAuth'

export function AppLayout() {
  const { state, logout } = useAuth()
  const [signingOut, setSigningOut] = useState(false)
  const [logoutError, setLogoutError] = useState(false)
  const username = state.status === 'ready' ? state.session.user?.username : undefined

  async function handleLogout() {
    setSigningOut(true)
    setLogoutError(false)
    try {
      await logout()
    } catch {
      setLogoutError(true)
    } finally {
      setSigningOut(false)
    }
  }

  return (
    <>
      <header className="app-header">
        <div className="app-header-inner">
          <nav aria-label="Main navigation">
            <Link to="/databases">Databases</Link>
          </nav>
          <div className="account">
            <span className="account-name">{username}</span>
            <button className="button-secondary" type="button" onClick={handleLogout} disabled={signingOut}>
              {signingOut ? 'Signing out…' : 'Sign out'}
            </button>
          </div>
        </div>
      </header>
      <main className="page">
        {logoutError && <p className="feedback feedback-error" role="alert">Could not sign out. Try again.</p>}
        <Outlet />
      </main>
    </>
  )
}
