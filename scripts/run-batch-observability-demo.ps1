param(
  [string]$BaseUrl = 'http://localhost:8080',
  [int]$DatabaseId = 2,
  [int]$ExtraRows = 250,
  [int]$BatchSize = 25
)

$ErrorActionPreference = 'Stop'

function New-AuthSession([string]$Username, [string]$Password) {
  $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $initial = Invoke-RestMethod -Method Get -Uri "$BaseUrl/api/auth/session" -WebSession $session
  $null = Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/auth/login" `
    -WebSession $session -Headers @{ 'X-CSRF-Token' = $initial.csrf_token } `
    -ContentType 'application/json' -Body (@{ username = $Username; password = $Password } | ConvertTo-Json)
  $authenticated = Invoke-RestMethod -Method Get -Uri "$BaseUrl/api/auth/session" -WebSession $session
  [pscustomobject]@{ Session = $session; Csrf = $authenticated.csrf_token }
}

Write-Host "Seeding $ExtraRows additional rows into demo_target..."
$lastId = 3 + $ExtraRows
$seedSql = "INSERT INTO demo_customers (id, name, email, active, notes) SELECT id, 'Batch Demo ' || id, 'batch-' || id || '@example.test', true, '' FROM generate_series(4, $lastId) AS id ON CONFLICT (id) DO NOTHING;"
docker compose exec -T database psql -U platform -d demo_target -v ON_ERROR_STOP=1 -c $seedSql
if ($LASTEXITCODE -ne 0) { throw 'Could not seed demo_target rows.' }

$editor = New-AuthSession 'demo_editor' 'demo-editor'
$migrationPayload = @{
  title = "Observability batch demo $(Get-Date -Format 'yyyyMMdd-HHmmss')"
  sql = 'UPDATE demo_customers SET notes = CONCAT(''batch-demo-'', id) WHERE id = ANY($1::bigint[]) RETURNING id'
  down_sql = 'UPDATE demo_customers SET notes = '''' WHERE id = ANY($1::bigint[]) RETURNING id'
  mode = 'batch'
  batch_config = @{
    scope_sql = 'SELECT id AS key FROM demo_customers WHERE active ORDER BY id'
    size = $BatchSize
    recovery = @{ schema = 'public'; table = 'demo_customers'; key = 'id' }
  }
} | ConvertTo-Json -Depth 10

$migration = Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/databases/$DatabaseId/migrations" `
  -WebSession $editor.Session -Headers @{ 'X-CSRF-Token' = $editor.Csrf } `
  -ContentType 'application/json' -Body $migrationPayload
Write-Host "Created batch migration $($migration.id): $($migration.title)"

$review = Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/databases/$DatabaseId/migrations/$($migration.id)/review" `
  -WebSession $editor.Session -Headers @{ 'X-CSRF-Token' = $editor.Csrf } `
  -ContentType 'application/json' `
  -Body (@{ operation = 'apply'; revision = 0; target_execution_id = '' } | ConvertTo-Json)
$requestId = $review.request_id
Write-Host "Created review $requestId"

foreach ($approval in @(
  @{ user = 'demo_leader'; password = 'demo-leader'; group = 'engineering' },
  @{ user = 'demo_auditor'; password = 'demo-auditor'; group = 'audit' }
)) {
  $groupId = ($review.decisions | Where-Object { $_.group_name -eq $approval.group }).group_id
  $approver = New-AuthSession $approval.user $approval.password
  $decisionBody = @{ group_id = [int]$groupId; decision = 'approved'; comment = 'Approved for observability batch demo.' } | ConvertTo-Json
  $review = Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/migration-reviews/$requestId/decision" `
    -WebSession $approver.Session -Headers @{ 'X-CSRF-Token' = $approver.Csrf } `
    -ContentType 'application/json' -Body $decisionBody
}

$job = Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/migration-reviews/$requestId/enqueue" `
  -WebSession $editor.Session -Headers @{ 'X-CSRF-Token' = $editor.Csrf } `
  -ContentType 'application/json' -Body '{}'
Write-Host "Enqueued job $($job.id); waiting for terminal state..."

$deadline = (Get-Date).AddSeconds(120)
do {
  Start-Sleep -Seconds 1
  $job = Invoke-RestMethod -Method Get -Uri "$BaseUrl/api/databases/$DatabaseId/jobs/$($job.id)" -WebSession $editor.Session
  Write-Host "  job $($job.id): $($job.state)"
} while ($job.state -notin @('succeeded', 'failed', 'partial', 'partially_reverted', 'recovered', 'compensated') -and (Get-Date) -lt $deadline)

if ($job.state -ne 'succeeded') { throw "Batch demo job ended in state '$($job.state)'." }

Write-Host ''
Write-Host 'Batch demo succeeded.'
Write-Host 'Open Grafana: http://localhost:3000/d/migration-platform/migration-platform-observability'
Write-Host 'The rows/batches panel should show data for the next few minutes.'
