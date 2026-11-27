package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type requestKey struct{}

type requestStatus struct {
	http.ResponseWriter
	code     int
	actor    int64
	database int64
}

func (r *requestStatus) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *requestStatus) WriteHeader(code int) {
	if code >= 200 && r.code == 0 {
		r.code = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *requestStatus) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

func WithActor(ctx context.Context, actor int64) context.Context {
	if request, ok := ctx.Value(requestKey{}).(*requestStatus); ok {
		request.actor = actor
	}
	field := attribute.Int64("actor_id", actor)
	trace.SpanFromContext(ctx).SetAttributes(field)
	return WithFields(ctx, field)
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := traceContext.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx = WithCorrelation(ctx, "")
		ctx, span := Start(ctx, "http.request", trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", r.Method)))
		status := &requestStatus{ResponseWriter: w}
		ctx = context.WithValue(ctx, requestKey{}, status)
		request := r.WithContext(ctx)
		w.Header().Set("X-Correlation-ID", CorrelationID(ctx))
		Log(ctx, slog.LevelInfo, "http.request", "method", r.Method, "status", "started")
		next.ServeHTTP(status, request)
		if status.code == 0 {
			status.code = http.StatusOK
		}
		span.SetAttributes(attribute.Int("http.response.status_code", status.code))
		if status.code >= 500 {
			Failure(ctx, "HTTP "+strconv.Itoa(status.code))
		}
		if status.actor != 0 {
			ctx = WithFields(ctx, attribute.Int64("actor_id", status.actor))
		}
		if status.database != 0 {
			ctx = WithFields(ctx, attribute.Int64("database_id", status.database))
		}
		Log(ctx, slog.LevelInfo, "http.result", "method", r.Method, "status", status.code,
			"status_class", fmt.Sprintf("%dxx", status.code/100))
		span.End(nil)
	})
}

func WithDatabase(ctx context.Context, id int64) context.Context {
	if request, ok := ctx.Value(requestKey{}).(*requestStatus); ok {
		request.database = id
	}
	field := attribute.Int64("database_id", id)
	trace.SpanFromContext(ctx).SetAttributes(field)
	return WithFields(ctx, field)
}
