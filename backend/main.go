// Customer backend: OpenTelemetry Go reading B3 headers. For each request it:
//  1. receives it and reads the trace from its B3 headers,
//  2. records a span in that trace and sends it to Tempo (what Grafana shows),
//  3. logs what it received and what it recorded.
package main

import (
	"context"  // root context for setup and shutdown
	"log"      // timestamped log lines on stderr
	"net/http" // HTTP server
	"os"       // environment variables
	"strings"  // lowercases header names for the log

	"go.opentelemetry.io/contrib/propagators/b3"                      // reads B3 headers (single and multi)
	"go.opentelemetry.io/otel/attribute"                              // span attributes (key/value tags)
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp" // sends spans to Tempo over OTLP/HTTP
	"go.opentelemetry.io/otel/propagation"                            // adapts http.Header for the propagator
	"go.opentelemetry.io/otel/sdk/resource"                           // describes this service (service.name)
	sdktrace "go.opentelemetry.io/otel/sdk/trace"                     // the tracer provider that creates and exports spans
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"                // standard attribute names
	"go.opentelemetry.io/otel/trace"                                  // span and span-context types
)

// The trace headers the backend logs and tags its span with, in display order.
var traceHeaders = []string{"traceparent", "b3", "X-B3-TraceId", "X-B3-SpanId", "X-B3-ParentSpanId", "X-B3-Sampled"}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds) // prefix every log line with HH:MM:SS.micros
	ctx := context.Background()                 // a root context for setup and shutdown
	// The OTLP/HTTP exporter: sends finished spans to localhost:4318 (Tempo), plain HTTP.
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithInsecure())
	if err != nil { // cannot build the exporter: give up
		log.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider( // the factory for spans
		sdktrace.WithSyncer(exp), // export each span the moment it ends (so it is in Tempo before we reply)
		sdktrace.WithResource(resource.NewSchemaless(semconv.ServiceName("b3-backend"))), // service.name shown in Grafana
	)
	defer tp.Shutdown(ctx)            // flush and close the exporter on exit
	tracer := tp.Tracer("w3c-b3-poc") // the tracer that creates this service's spans
	prop := b3.New()                  // the B3 reader: a B3-only backend

	// Called once per request, for every path.
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 1. Receive the request and read the trace from its B3 headers.
		rid := r.Header.Get("X-Request-Id")                                   // the client's request id, to follow one request across the logs
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header)) // read X-B3-* (or a single b3 header)
		parent := trace.SpanContextFromContext(ctx)                           // the trace/span the caller sent (invalid = none found)

		// 2. Record a span in that trace and send it to Tempo.
		_, span := tracer.Start(ctx, r.Method+" "+r.URL.Path, // a child of the caller's span, or a new root trace if there was none
			trace.WithSpanKind(trace.SpanKindServer),                      // marks it as the server side of a request
			trace.WithAttributes(attribute.String("poc.request_id", rid))) // tag it with the request id (visible in Grafana)
		for _, h := range traceHeaders { // tag it with every trace header received, too
			if v := r.Header.Get(h); v != "" {
				span.SetAttributes(attribute.String("poc.header."+strings.ToLower(h), v))
			}
		}
		sc := span.SpanContext() // this span's own trace id and span id
		span.End()               // finish it; with the syncer above, this exports it to Tempo now

		// 3. Log what was received and what was recorded.
		log.Printf("[backend] req=%s ← %s %s", rid, r.Method, r.URL.Path) // the arrival
		for _, h := range traceHeaders {                                  // each trace header received
			if v := r.Header.Get(h); v != "" {
				log.Printf("[backend] req=%s     %s: %s", rid, strings.ToLower(h), v)
			}
		}
		if parent.IsValid() { // the B3 headers carried a trace to continue
			log.Printf("[backend] req=%s   continued trace=%s from span=%s", rid, parent.TraceID(), parent.SpanID())
		} else { // nothing usable: the span started a trace of its own
			log.Printf("[backend] req=%s   no B3 trace received: started a new trace", rid)
		}
		if sc.IsSampled() { // exported to Tempo
			log.Printf("[backend] req=%s   span trace=%s span=%s sent to Tempo", rid, sc.TraceID(), sc.SpanID())
		} else { // X-B3-Sampled: 0 means the span is dropped
			log.Printf("[backend] req=%s   span trace=%s NOT sampled, not sent to Tempo", rid, sc.TraceID())
		}

		w.Header().Set("X-Trace-Id", sc.TraceID().String()) // tell the caller which trace this request was recorded under
	})
	addr := ":" + envOr("PORT", "8081")       // listen address, e.g. ":8081"
	log.Printf("backend %s (reads B3)", addr) // startup line
	log.Fatal(http.ListenAndServe(addr, nil)) // serve until killed
}

// envOr returns the environment variable k, or def when it is unset or empty.
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" { // set and non-empty
		return v
	}
	return def // fall back to the default
}
