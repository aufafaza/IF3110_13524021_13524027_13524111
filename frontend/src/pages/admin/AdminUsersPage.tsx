import { adminUsers } from '@/api/admin'
import { useRequest } from '@/api/useRequest'
import { useAuth } from '@/auth/useAuth'
import { RequestFeedback } from '@/components/RequestFeedback'
import { AdminFeedback } from './AdminFeedback'
import { readForm, useAdminAction } from './useAdminAction'

export function AdminUsersPage() {
  const { state } = useAuth()
  const userID = state.status === 'ready' ? state.session.user?.id : undefined
  const action = useAdminAction()
  const request = useRequest(adminUsers, action.revision)

  return (
    <>
      <h1>Users</h1>
      <AdminFeedback {...action} />
      <form className="editor-form" key={action.revision} onSubmit={(event) => {
        const form = readForm(event)
        void action.run('/api/users', {
          username: form.username, password: form.password, is_platform_admin: form.is_admin === 'on',
        }, { message: 'User created.' })
      }}>
        <h2>Create user</h2>
        <label>Username<input name="username" required autoComplete="off" /></label>
        <label>Password<input name="password" type="password" required autoComplete="new-password" /></label>
        <label className="checkbox-label"><input name="is_admin" type="checkbox" /> Platform administrator</label>
        <button disabled={action.busy}>Create user</button>
      </form>
      {request.status !== 'ready' ? <RequestFeedback request={request} loading="Loading users…" /> :
        <ul className="resource-list" role="list">
          {request.data.users.map((user) => (
            <li className="resource-row" key={user.id}>
              <strong>{user.username}</strong>
              <p>{user.is_active ? 'Active' : 'Inactive'} · {user.is_platform_admin ? 'Administrator' : 'Member'}</p>
              <div className="action-row">
                <button type="button" className="button-secondary" disabled={action.busy || user.id === userID}
                  onClick={() => void action.run(`/api/users/${user.id}/active`, { active: !user.is_active })}>
                  {user.is_active ? 'Deactivate' : 'Activate'}
                </button>
                <details>
                  <summary>Reset password</summary>
                  <form className="editor-form" onSubmit={(event) => {
                    const form = readForm(event)
                    void action.run(`/api/users/${user.id}/password`, { password: form.password }, {
                      message: 'Password reset.',
                    })
                  }}>
                    <label>New password<input name="password" type="password" required autoComplete="new-password" /></label>
                    <button disabled={action.busy}>Reset password</button>
                  </form>
                </details>
              </div>
            </li>
          ))}
        </ul>}
    </>
  )
}
