import http from 'k6/http'
import { check, sleep } from 'k6'
import { Counter, Rate, Trend } from 'k6/metrics'

const baseURL = (__ENV.BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '')
const username = __ENV.K6_USERNAME || ''
const password = __ENV.K6_PASSWORD || ''
const reviewIDs = (__ENV.K6_REVIEW_IDS || '').split(',').map((value) => value.trim()).filter(Boolean)
const databaseIDs = (__ENV.K6_DATABASE_IDS || '').split(',').map((value) => value.trim()).filter(Boolean)
const pollInterval = Number(__ENV.K6_POLL_INTERVAL_SECONDS || 1)
const pollTimeout = Number(__ENV.K6_POLL_TIMEOUT_SECONDS || 300)

const apiLatency = new Trend('api_latency_ms', true)
const queueWait = new Trend('migration_queue_wait_seconds', true)
const jobDuration = new Trend('migration_job_duration_seconds', true)
const submittedJobs = new Counter('migration_jobs_submitted_total')
const jobFailures = new Rate('migration_job_failure_rate')

const maxVUs = Number(__ENV.K6_MAX_VUS || 20)
const rampUp = __ENV.K6_RAMP_UP || '1m'
const hold = __ENV.K6_HOLD || '2m'
const rampDown = __ENV.K6_RAMP_DOWN || '1m'

export const options = {
  scenarios: {
    migration_stress: {
      executor: 'ramping-vus',
      startVUs: Number(__ENV.K6_START_VUS || 1),
      stages: [
        { duration: rampUp, target: maxVUs },
        { duration: hold, target: maxVUs },
        { duration: rampDown, target: 0 },
      ],
      gracefulRampDown: '30s',
    },
  },
  thresholds: {
    http_req_failed: [`rate<${__ENV.K6_MAX_HTTP_ERROR_RATE || 0.05}`],
    'api_latency_ms{operation:api_read}': [`p(95)<${__ENV.K6_API_P95_MS || 500}`],
    'api_latency_ms{operation:enqueue_job}': [`p(95)<${__ENV.K6_ENQUEUE_P95_MS || 1000}`],
    migration_job_failure_rate: [`rate<${__ENV.K6_MAX_JOB_FAILURE_RATE || 0.02}`],
  },
}

let csrfToken = ''
let cookieJar

function getCookieJar() {
  if (!cookieJar) cookieJar = http.cookieJar()
  return cookieJar
}

function authenticate() {
  const jar = getCookieJar()
  const session = http.get(`${baseURL}/api/auth/session`, { jar, tags: { operation: 'login_session' } })
  apiLatency.add(session.timings.duration, { operation: 'login_session' })
  if (!check(session, { 'login session is 200': (response) => response.status === 200 })) return false

  const token = session.json('csrf_token')
  const login = http.post(`${baseURL}/api/auth/login`, JSON.stringify({ username, password }), {
    jar,
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token },
    tags: { operation: 'login' },
  })
  apiLatency.add(login.timings.duration, { operation: 'login' })
  if (!check(login, { 'login is successful': (response) => response.status === 200 })) return false

  const authenticated = http.get(`${baseURL}/api/auth/session`, { jar, tags: { operation: 'login_session' } })
  apiLatency.add(authenticated.timings.duration, { operation: 'login_session' })
  if (!check(authenticated, { 'authenticated session is 200': (response) => response.status === 200 })) return false
  csrfToken = authenticated.json('csrf_token')
  return Boolean(csrfToken)
}

function pollJob(databaseID, jobID) {
  const jar = getCookieJar()
  const started = Date.now()
  let sawRunning = false
  let lastState = 'queued'
  let job
  while (Date.now() - started < pollTimeout * 1000) {
    const response = http.get(`${baseURL}/api/databases/${databaseID}/jobs/${jobID}`, {
      jar,
      tags: { operation: 'job_status' },
    })
    apiLatency.add(response.timings.duration, { operation: 'job_status' })
    const valid = check(response, { 'job status is available': (value) => value.status === 200 })
    if (!valid) {
      jobFailures.add(true)
      return false
    }
    job = response.json()
    lastState = job.state
    if (!sawRunning && !['queued', 'retrying'].includes(lastState)) {
      sawRunning = true
      queueWait.add((Date.now() - started) / 1000, { operation: job.operation || 'apply', mode: job.mode || 'ordinary' })
    }
    if (['succeeded', 'recovered', 'compensated', 'failed', 'partial', 'partially_reverted'].includes(lastState)) break
    sleep(pollInterval)
  }

  const elapsed = (Date.now() - started) / 1000
  jobDuration.add(elapsed, { operation: job?.operation || 'apply', mode: job?.mode || 'ordinary', result: lastState })
  const successful = ['succeeded', 'recovered', 'compensated'].includes(lastState)
  jobFailures.add(!successful)
  check(job, { 'migration job reaches a terminal state': () => successful || ['failed', 'partial', 'partially_reverted'].includes(lastState) })
  return successful
}

export function setup() {
  if (!username || !password) throw new Error('K6_USERNAME and K6_PASSWORD are required')
  if (!reviewIDs.length) throw new Error('K6_REVIEW_IDS must contain approved review request IDs')
  if (!databaseIDs.length) throw new Error('K6_DATABASE_IDS must contain database IDs matching the approved reviews')
  return { reviewIDs, databaseIDs }
}

export default function (data) {
  if (!csrfToken && !authenticate()) return

  const jar = getCookieJar()
  // Advance through approved reviews across iterations so an iteration never
  // silently re-enqueues the same already-completed job.
  const index = ((__ITER * maxVUs) + (__VU - 1)) % data.reviewIDs.length
  const databaseID = data.databaseIDs[index % data.databaseIDs.length]
  const reviewID = data.reviewIDs[index]
  const responses = http.batch([
    ['GET', `${baseURL}/api/databases/${databaseID}/tables`, null, { jar, tags: { operation: 'api_read' } }],
    ['POST', `${baseURL}/api/migration-reviews/${reviewID}/enqueue`, null, {
      jar,
      headers: { 'X-CSRF-Token': csrfToken },
      tags: { operation: 'enqueue_job' },
    }],
  ])

  const apiRead = responses[0]
  const enqueue = responses[1]
  apiLatency.add(apiRead.timings.duration, { operation: 'api_read' })
  apiLatency.add(enqueue.timings.duration, { operation: 'enqueue_job' })
  check(apiRead, { 'database API read is successful': (response) => response.status === 200 })

  const accepted = check(enqueue, {
    'job enqueue is accepted': (response) => response.status === 200 || response.status === 202,
  })
  if (!accepted) {
    jobFailures.add(true)
    return
  }
  submittedJobs.add(1)
  const jobID = enqueue.json('id')
  if (jobID) pollJob(databaseID, jobID)
  sleep(Number(__ENV.K6_ITERATION_PAUSE_SECONDS || 1))
}

export function handleSummary(data) {
  return { 'k6/results/summary.json': JSON.stringify(data, null, 2) }
}
