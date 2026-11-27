package observability

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics exposes only bounded labels. Database, user, job, execution, and
// SQL identifiers are deliberately excluded from metric labels.
type Metrics struct {
	registry          *prometheus.Registry
	requests          *prometheus.CounterVec
	requestDuration   *prometheus.HistogramVec
	jobStates         *prometheus.GaugeVec
	retries           *prometheus.CounterVec
	queueWait         *prometheus.HistogramVec
	lockWait          *prometheus.HistogramVec
	migrationDuration *prometheus.HistogramVec
	batches           *prometheus.CounterVec
	rows              *prometheus.CounterVec
	pools             *poolCollector
}

var defaultMetrics = NewMetrics()

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "migration_platform", Subsystem: "http", Name: "requests_total",
			Help: "HTTP requests handled by bounded route group, method, and status class.",
		}, []string{"route", "method", "status_class"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "migration_platform", Subsystem: "http", Name: "request_duration_seconds",
			Help:    "HTTP request duration by bounded route group, method, and status class.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "status_class"}),
		jobStates: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "migration_platform", Subsystem: "migration", Name: "jobs",
			Help: "Current migration jobs grouped by state, operation, and execution mode.",
		}, []string{"state", "operation", "mode"}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "migration_platform", Subsystem: "migration", Name: "retries_total",
			Help: "Migration attempts scheduled for retry.",
		}, []string{"operation", "mode"}),
		queueWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "migration_platform", Subsystem: "migration", Name: "queue_wait_seconds",
			Help:    "Time from migration job creation until a worker claim.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2, 5, 10, 30, 60, 300},
		}, []string{"operation", "mode"}),
		lockWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "migration_platform", Subsystem: "migration", Name: "lock_wait_seconds",
			Help:    "Time spent acquiring the target database migration lock.",
			Buckets: []float64{.001, .01, .05, .1, .25, .5, 1, 2, 5, 10, 30},
		}, []string{"operation", "mode"}),
		migrationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "migration_platform", Subsystem: "migration", Name: "duration_seconds",
			Help:    "Duration of a migration attempt grouped by operation, mode, and result.",
			Buckets: []float64{.01, .1, .5, 1, 2, 5, 10, 30, 60, 300, 900},
		}, []string{"operation", "mode", "result"}),
		batches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "migration_platform", Subsystem: "migration", Name: "batches_processed_total",
			Help: "Committed migration batches.",
		}, []string{"operation", "mode"}),
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "migration_platform", Subsystem: "migration", Name: "rows_processed_total",
			Help: "Rows committed by batched migrations.",
		}, []string{"operation", "mode"}),
	}
	m.pools = newPoolCollector()
	registry.MustRegister(m.requests, m.requestDuration, m.jobStates, m.retries, m.queueWait,
		m.lockWait, m.migrationDuration, m.batches, m.rows, m.pools)
	return m
}

func DefaultMetrics() *Metrics { return defaultMetrics }

func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(defaultMetrics.registry, promhttp.HandlerOpts{})
}

func ObserveHTTP(method, route string, status int, duration time.Duration) {
	statusClass := "5xx"
	if status >= 100 && status < 600 {
		statusClass = string(rune('0'+status/100)) + "xx"
	}
	defaultMetrics.requests.WithLabelValues(route, method, statusClass).Inc()
	defaultMetrics.requestDuration.WithLabelValues(route, method, statusClass).Observe(duration.Seconds())
}

func ObserveQueueWait(operation, mode string, duration time.Duration) {
	defaultMetrics.queueWait.WithLabelValues(metricOperation(operation), metricMode(mode)).Observe(duration.Seconds())
}

func ObserveLockWait(operation, mode string, duration time.Duration) {
	defaultMetrics.lockWait.WithLabelValues(metricOperation(operation), metricMode(mode)).Observe(duration.Seconds())
}

func ObserveMigration(operation, mode, result string, duration time.Duration) {
	defaultMetrics.migrationDuration.WithLabelValues(metricOperation(operation), metricMode(mode), result).Observe(duration.Seconds())
}

func ObserveRetry(operation, mode string) {
	defaultMetrics.retries.WithLabelValues(metricOperation(operation), metricMode(mode)).Inc()
}

func ObserveBatch(operation, mode string, rows int64) {
	defaultMetrics.batches.WithLabelValues(metricOperation(operation), metricMode(mode)).Inc()
	if rows > 0 {
		defaultMetrics.rows.WithLabelValues(metricOperation(operation), metricMode(mode)).Add(float64(rows))
	}
}

type JobState struct {
	State     string
	Operation string
	Mode      string
	Count     int
}

func SetJobStates(states []JobState) {
	defaultMetrics.jobStates.Reset()
	for _, state := range states {
		defaultMetrics.jobStates.WithLabelValues(metricState(state.State), metricOperation(state.Operation), metricMode(state.Mode)).Set(float64(state.Count))
	}
}

func RegisterPool(role string, pool *pgxpool.Pool) {
	defaultMetrics.pools.Register(role, pool)
}

func UnregisterPool(pool *pgxpool.Pool) {
	defaultMetrics.pools.Unregister(pool)
}

func RouteGroup(path string) string {
	switch {
	case path == "/healthz":
		return "health"
	case path == "/metrics":
		return "metrics"
	case strings.HasPrefix(path, "/api/"):
		return "api"
	case strings.HasPrefix(path, "/admin"):
		return "admin"
	case strings.HasPrefix(path, "/static/"):
		return "static"
	default:
		return "page"
	}
}

func metricOperation(operation string) string {
	switch operation {
	case "apply", "rollback", "forward", "batch_recovery", "compensation":
		return operation
	default:
		return "other"
	}
}

func metricMode(mode string) string {
	if mode == "batch" {
		return "batch"
	}
	return "ordinary"
}

func metricState(state string) string {
	switch state {
	case "queued", "running", "reconciling", "reporting", "retrying", "partial", "partially_reverted", "recovered", "compensated", "succeeded", "failed":
		return state
	default:
		return "other"
	}
}

type poolCollector struct {
	mu     sync.RWMutex
	pools  map[*pgxpool.Pool]string
	active *prometheus.Desc
	total  *prometheus.Desc
	wait   *prometheus.Desc
}

func newPoolCollector() *poolCollector {
	return &poolCollector{
		pools:  make(map[*pgxpool.Pool]string),
		active: prometheus.NewDesc("migration_platform_db_pool_connections", "Database pool connections by role and state.", []string{"pool", "state"}, nil),
		total:  prometheus.NewDesc("migration_platform_db_pool_total_connections", "Total database pool connections by role.", []string{"pool"}, nil),
		wait:   prometheus.NewDesc("migration_platform_db_pool_acquire_wait_seconds", "Cumulative time spent waiting for a database pool connection by role.", []string{"pool"}, nil),
	}
}

func (c *poolCollector) Register(role string, pool *pgxpool.Pool) {
	if role != "platform" && role != "target" {
		return
	}
	c.mu.Lock()
	c.pools[pool] = role
	c.mu.Unlock()
}

func (c *poolCollector) Unregister(pool *pgxpool.Pool) {
	c.mu.Lock()
	delete(c.pools, pool)
	c.mu.Unlock()
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.active
	ch <- c.total
	ch <- c.wait
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	type aggregate struct {
		active int32
		idle   int32
		total  int32
		wait   time.Duration
	}
	aggregates := map[string]aggregate{"platform": {}, "target": {}}
	c.mu.RLock()
	for pool, role := range c.pools {
		stat := pool.Stat()
		value := aggregates[role]
		value.active += stat.AcquiredConns()
		value.idle += stat.IdleConns()
		value.total += stat.TotalConns()
		value.wait += stat.EmptyAcquireWaitTime()
		aggregates[role] = value
	}
	c.mu.RUnlock()
	for role, value := range aggregates {
		ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(value.active), role, "active")
		ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(value.idle), role, "idle")
		ch <- prometheus.MustNewConstMetric(c.total, prometheus.GaugeValue, float64(value.total), role)
		ch <- prometheus.MustNewConstMetric(c.wait, prometheus.GaugeValue, value.wait.Seconds(), role)
	}
}
