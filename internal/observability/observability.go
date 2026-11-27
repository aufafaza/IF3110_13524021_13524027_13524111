package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type correlationKey struct{}
type fieldsKey struct{}

var correlationFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var traceContext propagation.TraceContext

type Propagation struct {
	Traceparent   string
	Tracestate    string
	CorrelationID string
}

func WithCorrelation(ctx context.Context, id string) context.Context {
	if !correlationFormat.MatchString(id) {
		var seed [16]byte
		_, _ = rand.Read(seed[:])
		id = hex.EncodeToString(seed[:])
	}
	return context.WithValue(ctx, correlationKey{}, id)
}

func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

func Capture(ctx context.Context) Propagation {
	carrier := propagation.MapCarrier{}
	traceContext.Inject(ctx, carrier)
	return Propagation{
		Traceparent: carrier.Get("traceparent"), Tracestate: carrier.Get("tracestate"),
		CorrelationID: CorrelationID(ctx),
	}
}

func Restore(ctx context.Context, saved Propagation) context.Context {
	ctx = traceContext.Extract(ctx, propagation.MapCarrier{
		"traceparent": saved.Traceparent, "tracestate": saved.Tracestate,
	})
	if saved.CorrelationID != "" || CorrelationID(ctx) == "" {
		ctx = WithCorrelation(ctx, saved.CorrelationID)
	}
	return ctx
}

func WithFields(ctx context.Context, fields ...attribute.KeyValue) context.Context {
	existing, _ := ctx.Value(fieldsKey{}).([]attribute.KeyValue)
	set := attribute.NewSet(append(append([]attribute.KeyValue(nil), existing...), fields...)...)
	return context.WithValue(ctx, fieldsKey{}, set.ToSlice())
}

type Span struct{ trace.Span }

func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, *Span) {
	if CorrelationID(ctx) == "" {
		ctx = WithCorrelation(ctx, "")
	}
	fields, _ := ctx.Value(fieldsKey{}).([]attribute.KeyValue)
	opts = append(opts, trace.WithAttributes(append([]attribute.KeyValue{
		attribute.String("correlation_id", CorrelationID(ctx)),
	}, fields...)...))
	ctx, span := otel.Tracer("migration-platform").Start(ctx, name, opts...)
	return ctx, &Span{Span: span}
}

func (s *Span) End(err error) {
	if err != nil {
		message := safeError(err)
		s.RecordError(errors.New(message))
		s.SetStatus(codes.Error, message)
	}
	s.Span.End()
}

func Failure(ctx context.Context, code string) {
	trace.SpanFromContext(ctx).SetStatus(codes.Error, code)
}

func Log(ctx context.Context, level slog.Level, event string, attrs ...any) {
	fields, _ := ctx.Value(fieldsKey{}).([]attribute.KeyValue)
	for _, field := range fields {
		attrs = append(attrs, string(field.Key), field.Value.AsInterface())
	}
	span := trace.SpanContextFromContext(ctx)
	attrs = append(attrs, "event", event, "correlation_id", CorrelationID(ctx))
	if span.IsValid() {
		attrs = append(attrs, "trace_id", span.TraceID().String(), "span_id", span.SpanID().String())
	}
	slog.Default().Log(ctx, level, event, attrs...)
}

func ConfigureJSON(w io.Writer) {
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	log.SetOutput(stdLogWriter{logger: logger})
	log.SetFlags(0)
}

type stdLogWriter struct{ logger *slog.Logger }

func (w stdLogWriter) Write(p []byte) (int, error) {
	w.logger.Info("legacy_log", "message", strings.TrimSpace(string(p)))
	return len(p), nil
}

func ConfigureTracing() func(context.Context) error {
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "migration-platform"))),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(spanLogger{})),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown
}

type spanLogger struct{}

func (spanLogger) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		id := span.SpanContext()
		attrs := []any{
			"event", "span.end", "span_name", span.Name(),
			"trace_id", id.TraceID().String(), "span_id", id.SpanID().String(),
			"status", span.Status().Code.String(),
			"duration_ms", float64(span.EndTime().Sub(span.StartTime()).Microseconds()) / 1000,
		}
		if parent := span.Parent(); parent.IsValid() {
			attrs = append(attrs, "parent_span_id", parent.SpanID().String())
		}
		if span.Status().Description != "" {
			attrs = append(attrs, "failure", span.Status().Description)
		}
		for _, field := range span.Attributes() {
			attrs = append(attrs, string(field.Key), field.Value.AsInterface())
		}
		slog.InfoContext(ctx, "span.end", attrs...)
	}
	return nil
}

func (spanLogger) Shutdown(context.Context) error { return nil }

func SQLState(err error) string {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return state.SQLState()
	}
	return ""
}

func safeError(err error) string {
	if code := SQLState(err); code != "" {
		return "PostgreSQL SQLSTATE " + code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "operation canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "operation timed out"
	default:
		return fmt.Sprintf("%T", err)
	}
}
