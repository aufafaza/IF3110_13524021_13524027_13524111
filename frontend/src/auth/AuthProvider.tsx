import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { AuthContext, type AuthContextValue } from '@/auth/useAuth'
import { getSession, postLogin, postLogout } from '@/api/auth'

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthContextValue['state']>({
    status: 'loading',
  })

  const refreshSession = useCallback(async () => {
    try {
      const session = await getSession()
      setState({ status: 'ready', session })
    } catch (error) {
      setState({
        status: 'error',
        message: error instanceof Error ? error.message : 'Failed to load session.',
      })
    }
  }, [])

  useEffect(() => {
    void refreshSession()
  }, [refreshSession])

  const login: AuthContextValue['login'] = async (username, password) => {
    if (state.status != 'ready' || state.session.authenticated) { 
      throw new Error('Login requires anonymous session.') 
    } 

    await postLogin(username, password, state.session.csrf_token)
    await refreshSession() 
  }

  const logout: AuthContextValue['logout'] = async () => {
    if (state.status != 'ready' || !state.session.authenticated) { 
      throw new Error('Logout requires authenticated session.') 
    } 

    await postLogout(state.session.csrf_token) 
    await refreshSession() 
  }



  return (
    <AuthContext.Provider value={{ state, refreshSession, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}
