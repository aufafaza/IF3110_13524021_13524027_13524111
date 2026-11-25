import { Link, useNavigate, useParams } from 'react-router-dom'
import { adminDatabases, adminGroup, adminGroups, adminUsers } from '@/api/admin'
import { useRequest } from '@/api/useRequest'
import { RequestFeedback } from '@/components/RequestFeedback'
import { AdminFeedback } from './AdminFeedback'
import { readForm, useAdminAction } from './useAdminAction'

export function AdminGroupsPage() {
  const action = useAdminAction()
  const request = useRequest(adminGroups, action.revision)
  return (
    <>
      <h1>Groups</h1>
      <AdminFeedback {...action} />
      <form className="editor-form" key={action.revision} onSubmit={(event) => {
        const form = readForm(event)
        void action.run('/api/groups', { name: form.name }, { message: 'Group created.' })
      }}>
        <h2>Create group</h2>
        <label>Name<input name="name" required /></label>
        <button disabled={action.busy}>Create group</button>
      </form>
      {request.status !== 'ready' ? <RequestFeedback request={request} loading="Loading groups…" /> :
        request.data.groups.length === 0 ? <p className="feedback">No groups available.</p> :
          <ul className="resource-list" role="list">
            {request.data.groups.map((group) => (
              <li key={group.ID}><Link className="resource-link" to={`/admin/groups/${group.ID}`}>{group.Name}</Link></li>
            ))}
          </ul>}
    </>
  )
}

export function AdminGroupPage() {
  const { id } = useParams()
  return id && /^[1-9]\d*$/.test(id) ? <GroupEditor key={id} id={id} /> : <h1>Group not found</h1>
}

function GroupEditor({ id }: { id: string }) {
  const navigate = useNavigate()
  const action = useAdminAction()
  const request = useRequest(adminGroup, id, action.revision)
  const users = useRequest(adminUsers, 0)
  const databases = useRequest(adminDatabases, 0)
  const path = `/api/groups/${id}`

  return (
    <>
      <Link className="back-link" to="/admin/groups">Back to groups</Link>
      <AdminFeedback {...action} />
      {request.status !== 'ready' ? <RequestFeedback request={request} loading="Loading group…" /> : (
        <>
          <h1>{request.data.group.Name}</h1>
          <h2>Members</h2>
          {(request.data.members ?? []).map((member) => (
            <div className="action-row" key={member.ID}>
              <span>{member.Username}</span>
              <button className="button-secondary" disabled={action.busy} onClick={() =>
                void action.run(`${path}/members`, { user_id: member.ID, action: 'remove' })
              }>Remove</button>
            </div>
          ))}
          {users.status !== 'ready' ? <RequestFeedback request={users} loading="Loading users…" /> : (
            <form className="editor-form" onSubmit={(event) => {
              const form = readForm(event)
              void action.run(`${path}/members`, { user_id: Number(form.user_id), action: 'add' })
            }}>
              <label>User<select name="user_id" required defaultValue="">
                <option value="">Select a user</option>
                {users.data.users.filter((user) => !request.data.members?.some((member) => member.ID === user.ID))
                  .map((user) => <option key={user.ID} value={user.ID}>{user.Username}</option>)}
              </select></label>
              <button disabled={action.busy}>Add member</button>
            </form>
          )}
          <h2>Database grants</h2>
          {(request.data.grants ?? []).map((grant) => (
            <div className="action-row" key={grant.DatabaseID}>
              <span>{grant.DatabaseName} · {grant.Level === 2 ? 'Edit' : 'View'}</span>
              <button className="button-secondary" disabled={action.busy} onClick={() =>
                void action.run(`${path}/grants`, { database_id: grant.DatabaseID, action: 'remove' })
              }>Remove grant</button>
            </div>
          ))}
          {databases.status !== 'ready' ? <RequestFeedback request={databases} loading="Loading databases…" /> : (
            <form className="editor-form" onSubmit={(event) => {
              const form = readForm(event)
              void action.run(`${path}/grants`, { database_id: Number(form.database_id), level: form.level, action: 'set' })
            }}>
              <label>Database<select name="database_id" required defaultValue="">
                <option value="">Select a database</option>
                {databases.data.databases.map((db) => <option key={db.id} value={db.id}>{db.name}</option>)}
              </select></label>
              <label>Access<select name="level"><option value="view">View</option><option value="edit">Edit</option></select></label>
              <button disabled={action.busy}>Save grant</button>
            </form>
          )}
          <button className="button-secondary" disabled={action.busy} onClick={() => {
            if (window.confirm('Delete this group and remove its memberships and grants?')) {
              void action.run(path, undefined, { method: 'DELETE', onSuccess: () => navigate('/admin/groups') })
            }
          }}>Delete group</button>
        </>
      )}
    </>
  )
}
