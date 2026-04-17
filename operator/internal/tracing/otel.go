/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package tracing — OpenTelemetry bootstrap for the nyx-operator (#471 part B).
//
// Mirrors the Python-side shared/otel.py shape: a single InitOTel that is a
// no-op unless OTEL_ENABLED is truthy, plus a Tracer() helper that always
// returns a usable tracer (real or no-op) so call sites never need to gate on
// otel-enabled.
//
// Standard OTel env vars apply unchanged: OTEL_EXPORTER_OTLP_ENDPOINT,
// OTEL_TRACES_SAMPLER, etc. Resource attributes added on top:
//
//	service.name=nyx-operator (overridable via OTEL_SERVICE_NAME)
//	k8s.namespace (POD_NAMESPACE if set)
//	k8s.pod.name  (POD_NAME if set)
package tracing

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// TracerName is the package-level name for the operator's tracer.
	// All spans emitted by the operator come from this tracer.
	TracerName = "github.com/nyx-ai/nyx-operator"

	defaultServiceName = "nyx-operator"
)

// enabledEnv reports whether OTel should be initialised. Mirrors the Python
// shim's "0/false/no/off/empty all disable" convention.
func enabledEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_ENABLED")))
	switch v {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// InitOTel initialises a TracerProvider + OTLP/HTTP exporter when OTEL_ENABLED
// is truthy. Returns a shutdown function the caller MUST defer (no-op when
// OTel was never enabled). Safe to call once per process; calling again is a
// no-op.
func InitOTel(ctx context.Context) (func(context.Context) error, error) {
	log := logf.FromContext(ctx).WithName("otel-init")
	// noopShutdown is returned on every error path so callers can unconditionally
	// defer the shutdown closure without a nil check (#484).
	noopShutdown := func(context.Context) error { return nil }
	if !enabledEnv() {
		log.V(1).Info("OTEL_ENABLED unset/false — skipping OTel init (call sites will use the no-op tracer)")
		return noopShutdown, nil
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return noopShutdown, fmt.Errorf("create OTLP HTTP exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName()),
			semconv.K8SNamespaceName(envOr("POD_NAMESPACE", "")),
			semconv.K8SPodName(envOr("POD_NAME", "")),
		),
	)
	if err != nil {
		return noopShutdown, fmt.Errorf("merge resource attributes: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	// W3C trace-context propagator matches the Python side (#468/#469) so
	// inbound traceparent headers and outbound child spans correlate.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.Info("OTel tracing initialised",
		"service.name", serviceName(),
		"endpoint_env", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	return tp.Shutdown, nil
}

// Tracer returns the operator's tracer. When OTel hasn't been initialised
// (e.g. OTEL_ENABLED=false), returns the OTel SDK's built-in no-op tracer so
// call sites can always do `Tracer().Start(...)` without nil checks.
func Tracer() trace.Tracer {
	if tp := otel.GetTracerProvider(); tp != nil {
		return tp.Tracer(TracerName)
	}
	return noop.NewTracerProvider().Tracer(TracerName)
}

func serviceName() string {
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		return v
	}
	return defaultServiceName
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
