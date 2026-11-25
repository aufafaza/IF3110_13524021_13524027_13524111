import { Navigate, Route, Routes } from 'react-router-dom'
import { RequireAuth } from '@/auth/RequireAuth'
import { AppLayout } from '@/layout/AppLayout'
import { DatabaseListPage } from '@/pages/DatabaseListPage'
import { LoginPage } from '@/pages/LoginPage'
import { DatabaseTablesPage } from './pages/DatabaseTablesPage'
import { TableDetailPage } from '@/pages/TableDetailPage'
import { MigrationsPage } from '@/pages/MigrationsPage'

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/databases" replace />} />
      <Route path="/login" element={<LoginPage />} />
      <Route element={<RequireAuth />}>
        <Route element={<AppLayout />}>
          <Route path="/databases" element={<DatabaseListPage />} />
          <Route path="/databases/:dbID" element={<DatabaseTablesPage />} />
          <Route path="/databases/:dbID/table" element={<TableDetailPage />} />
          <Route path="/databases/:dbID/migrations" element={<MigrationsPage />} />
          <Route path="/databases/:dbID/migrations/:migrationID" element={<MigrationsPage />} />
        </Route>
      </Route>
      <Route path="*" element={<h1>Page not found</h1>} />
    </Routes>
  )
}
