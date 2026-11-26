import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listDatabases, type Database } from '@/api/databases'

export function DatabaseListPage() {
  const [databases, setDatabases] = useState<Database[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    async function loadDatabases() {
      try {
        const { databases } = await listDatabases(controller.signal)
        if (!controller.signal.aborted) setDatabases(databases)
      } catch {
        if (!controller.signal.aborted) setError('Could not load databases.')
      }
    }

    void loadDatabases()
    return () => controller.abort()
  }, [])

  return (
    <>
      <h1>Databases</h1>
      {error && <p className="feedback feedback-error" role="alert">{error}</p>}
      {!error && databases === null && <p className="feedback" role="status">Loading databases…</p>}
      {databases?.length === 0 && <p className="feedback">No databases available.</p>}
      {databases && databases.length > 0 && (
        <ul className="resource-list" role="list">
          {databases.map((database) => (
            <li key={database.id}>
              <Link className="resource-link" to={`/databases/${database.id}`}>{database.name}</Link>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}
