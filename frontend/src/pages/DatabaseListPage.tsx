type Database = {
  id: number
  name: string
}

const databases: Database[] = [
  // TODO: Add fixture databases until the Go list API is ready.
]

export function DatabaseListPage() {
  return (
    <>
      <h1>Databases</h1>
      {databases.length === 0 ? <p>No databases available.</p> : (
        <ul>
          {databases.map((database) => <li key={database.id}>{database.name}</li>)}
        </ul>
      )}
    </>
  )
}
