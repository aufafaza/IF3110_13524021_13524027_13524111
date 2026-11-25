import { Navigate, Route, Routes } from 'react-router-dom'
import { RequireAuth } from '@/auth/RequireAuth'
import { AppLayout } from '@/layout/AppLayout'
import { DatabaseListPage } from '@/pages/DatabaseListPage'
import { LoginPage } from '@/pages/LoginPage'

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/databases" replace />} />
      <Route path="/login" element={<LoginPage />} />
      <Route element={<RequireAuth />}>
        <Route element={<AppLayout />}>
          <Route path="/databases" element={<DatabaseListPage />} />
        </Route>
      </Route>
      <Route path="*" element={<h1>Page not found</h1>} />
    </Routes>
  )
}
