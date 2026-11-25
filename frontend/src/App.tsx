import { Navigate, Route, Routes } from 'react-router-dom'
import { RequireAuth } from '@/auth/RequireAuth'
import { RequireAdmin } from '@/auth/RequireAdmin'
import { AppLayout } from '@/layout/AppLayout'
import { DatabaseListPage } from '@/pages/DatabaseListPage'
import { LoginPage } from '@/pages/LoginPage'
import { DatabaseTablesPage } from './pages/DatabaseTablesPage'
import { TableDetailPage } from '@/pages/TableDetailPage'
import { MigrationsPage } from '@/pages/MigrationsPage'
import { EditMigrationPage, NewMigrationPage } from '@/pages/MigrationEditor'
import { AdminUsersPage } from '@/pages/admin/AdminUsersPage'
import { AdminGroupPage, AdminGroupsPage } from '@/pages/admin/AdminGroupsPage'
import { AdminDatabasePage, AdminDatabasesPage, NewAdminDatabasePage } from '@/pages/admin/AdminDatabasesPage'

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
          <Route path="/databases/:dbID/migrations/new" element={<NewMigrationPage />} />
          <Route path="/databases/:dbID/migrations/:migrationID/edit" element={<EditMigrationPage />} />
          <Route element={<RequireAdmin />}>
            <Route path="/admin/users" element={<AdminUsersPage />} />
            <Route path="/admin/groups" element={<AdminGroupsPage />} />
            <Route path="/admin/groups/:id" element={<AdminGroupPage />} />
            <Route path="/admin/databases" element={<AdminDatabasesPage />} />
            <Route path="/admin/databases/new" element={<NewAdminDatabasePage />} />
            <Route path="/admin/databases/:id" element={<AdminDatabasePage />} />
          </Route>
        </Route>
      </Route>
      <Route path="*" element={<h1>Page not found</h1>} />
    </Routes>
  )
}
