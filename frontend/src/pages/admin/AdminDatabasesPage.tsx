import { Link, useNavigate, useParams } from 'react-router-dom'
import { adminDatabase, adminDatabases, type ManagedDatabase } from '@/api/admin'
import { useRequest } from '@/api/useRequest'
import { RequestFeedback } from '@/components/RequestFeedback'
import { AdminFeedback } from './AdminFeedback'
import { readForm, useAdminAction } from './useAdminAction'

const sslModes = ['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full']

export function AdminDatabasesPage() {
  const request = useRequest(adminDatabases, 0)
  return (
    <>
      <h1>Manage databases</h1>
      <Link className="back-link" to="/admin/databases/new">Add database</Link>
      {request.status !== 'ready' ? <RequestFeedback request={request} loading="Loading databases…" /> :
        request.data.databases.length === 0 ? <p className="feedback">No databases configured.</p> :
          <ul className="resource-list" role="list">
            {request.data.databases.map((db) => (
              <li key={db.id}><Link className="resource-link" to={`/admin/databases/${db.id}`}>{db.name}</Link></li>
            ))}
          </ul>}
    </>
  )
}

export function AdminDatabasePage() {
  const { id } = useParams()
  return (
    <>
      <Link className="back-link" to="/admin/databases">Back to managed databases</Link>
      {id && /^[1-9]\d*$/.test(id) ? <EditDatabase key={id} id={id} /> : <h1>Database not found</h1>}
    </>
  )
}

export function NewAdminDatabasePage() {
  return (
    <>
      <Link className="back-link" to="/admin/databases">Back to managed databases</Link>
      <h1>Add database</h1>
      <DatabaseForm />
    </>
  )
}

function EditDatabase({ id }: { id: string }) {
  const request = useRequest(adminDatabase, id)
  if (request.status !== 'ready') return <RequestFeedback request={request} loading="Loading database…" />
  return <><h1>Edit {request.data.name}</h1><DatabaseForm database={request.data} /></>
}

function DatabaseForm({ database }: { database?: ManagedDatabase }) {
  const action = useAdminAction()
  const navigate = useNavigate()
  const collection = '/api/admin/databases'
  const path = database ? `${collection}/${database.id}` : collection
  return (
    <>
      <AdminFeedback {...action} />
      <form className="editor-form" key={action.revision} onSubmit={(event) => {
        const fields = readForm(event)
        const test = event.nativeEvent.submitter?.getAttribute('value') === 'test'
        const input = { ...fields, port: Number(fields.port) }
        if (test) {
          void action.run(`${collection}/test`, input, { reload: false, message: 'Connection successful.' })
        } else {
          void action.run(path, input, {
            method: database ? 'PUT' : 'POST',
            onSuccess: () => navigate('/admin/databases'),
          })
        }
      }}>
        <label>Name<input name="name" required maxLength={64} defaultValue={database?.name} /></label>
        <label>Host<input name="host" required defaultValue={database?.host} /></label>
        <label>Port<input name="port" type="number" min={1} max={65535} required defaultValue={database?.port ?? 5432} /></label>
        <label>Database name<input name="db_name" required defaultValue={database?.db_name} /></label>
        <label>User<input name="db_user" required defaultValue={database?.db_user} /></label>
        <label>
          {database ? 'New password (optional)' : 'Password'}
          <input name="password" type="password" required={!database} autoComplete="new-password" />
        </label>
        <label>SSL mode<select name="sslmode" defaultValue={database?.sslmode ?? 'prefer'}>
          {sslModes.map((mode) => <option key={mode}>{mode}</option>)}
        </select></label>
        <div className="action-row">
          <button value="save" disabled={action.busy}>{database ? 'Save changes' : 'Add database'}</button>
          <button className="button-secondary" value="test" disabled={action.busy}>Test connection settings</button>
          {database && (
            <button type="button" className="button-secondary" disabled={action.busy} onClick={() =>
              void action.run(`${path}/test`, undefined, { reload: false, message: 'Saved connection is reachable.' })
            }>Test saved connection</button>
          )}
        </div>
      </form>
      {database && (
        <button className="button-secondary" disabled={action.busy} onClick={() => {
          if (window.confirm('Remove this connection and its grants and migration history?')) {
            void action.run(path, undefined, { method: 'DELETE', onSuccess: () => navigate('/admin/databases') })
          }
        }}>Remove database</button>
      )}
    </>
  )
}
