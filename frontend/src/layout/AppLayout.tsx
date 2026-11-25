import { Link, Outlet } from 'react-router-dom'
import { useAuth } from '@/auth/useAuth'

export function AppLayout() {
  const { state, logout } = useAuth()
  const username = state.status === 'ready' ? state.session.user?.username : undefined

  // function handleLogout() {
  //   // TODO: Call logout() and show an error if it fails.
  // } 

   
  const handleLogout  = async() => { 
    try {
      await logout()
    } catch (error) {
      throw new Error("Logout failed") 
    }
  } 
  return (
    <>
      <header className="app-header">
        <nav aria-label="Main navigation">
          <Link to="/databases">Databases</Link>
        </nav>
        <span>{username}</span>
        <button type="button" onClick={handleLogout}>Sign out</button>
      </header>
      <main className="page">
        <Outlet />
      </main>
    </>
  )
}
