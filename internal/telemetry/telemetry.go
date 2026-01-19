package telemetry

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

var shutdownFn func(context.Context) error
var logProvider *sdklog.LoggerProvider

// Setup initializes the OpenTelemetry tracer provider using environment variables.
// It reads OTEL_EXPORTER_OTLP_* and OTEL_SERVICE_NAME by default.
// Returns a Shutdown function that should be called before process exit.
func Setup(ctx context.Context) (func(context.Context) error, error) {
	// If no OTLP endpoint is configured at all, skip telemetry entirely (opt-in)
	if !hasAnyOTLPConfig() {
		shutdownFn = nil
		logProvider = nil
		return nil, nil
	}
	// Determine which signals are enabled
	useTraces := isTracesConfigured()
	useLogs := isLogsConfigured()

	var (
		trProto, lgProto       string
		trEP, lgEP             string
		trInsecure, lgInsecure bool
		trHeaders, lgHeaders   map[string]string
		tp                     *sdktrace.TracerProvider
	)
	if useTraces {
		trProto = resolveProtocol("traces")
		trEP, trInsecure, _ = resolveEndpoint("traces", trProto)
		trHeaders = resolveHeaders("traces")
	}
	if useLogs {
		lgProto = resolveProtocol("logs")
		lgEP, lgInsecure, _ = resolveEndpoint("logs", lgProto)
		lgHeaders = resolveHeaders("logs")
	}

	// Resource attributes: from env + standard host/OS/process + default service name
	svc := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if svc == "" {
		svc = "msk-account-cli"
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcess(),
		resource.WithAttributes(
			semconv.ServiceName(svc),
		),
	)
	if err != nil {
		return nil, err
	}

	// Tracer exporter/provider with batcher (flush interval reasonable for CLI)
	if useTraces {
		var exporter sdktrace.SpanExporter
		switch trProto {
		case "http", "http/protobuf":
			hOpts := []otlptracehttp.Option{}
			host, scheme, path := parseURL(trEP)
			if host != "" {
				hOpts = append(hOpts, otlptracehttp.WithEndpoint(host))
			}
			if path != "" && path != "/" {
				hOpts = append(hOpts, otlptracehttp.WithURLPath(path))
			}
			if len(trHeaders) > 0 {
				hOpts = append(hOpts, otlptracehttp.WithHeaders(trHeaders))
			}
			if trInsecure || scheme == "http" {
				hOpts = append(hOpts, otlptracehttp.WithInsecure())
			}
			exp, e := otlptracehttp.New(ctx, hOpts...)
			if e != nil {
				return nil, e
			}
			exporter = exp
		default: // grpc
			tOpts := []otlptracegrpc.Option{}
			if trEP != "" {
				tOpts = append(tOpts, otlptracegrpc.WithEndpoint(trEP))
			}
			if trInsecure {
				tOpts = append(tOpts, otlptracegrpc.WithInsecure())
			}
			if len(trHeaders) > 0 {
				tOpts = append(tOpts, otlptracegrpc.WithHeaders(trHeaders))
			}
			exp, e := otlptracegrpc.New(ctx, tOpts...)
			if e != nil {
				return nil, e
			}
			exporter = exp
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(2*time.Second)),
			sdktrace.WithResource(res),
		)

		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	// Logs exporter and provider (optional; proceed if creation fails)
	// Initialize only if at least an endpoint is discoverable, otherwise skip silently.
	if useLogs && (lgEP != "" || lgProto != "grpc") { // explicit http allowed; otherwise requires endpoint
		switch lgProto {
		case "http", "http/protobuf":
			host, scheme, path := parseURL(lgEP)
			lOpts := []otlploghttp.Option{}
			if host != "" {
				lOpts = append(lOpts, otlploghttp.WithEndpoint(host))
			}
			if path != "" && path != "/" {
				lOpts = append(lOpts, otlploghttp.WithURLPath(path))
			}
			if len(lgHeaders) > 0 {
				lOpts = append(lOpts, otlploghttp.WithHeaders(lgHeaders))
			}
			if lgInsecure || scheme == "http" {
				lOpts = append(lOpts, otlploghttp.WithInsecure())
			}
			if logExp, err := otlploghttp.New(ctx, lOpts...); err == nil {
				logProvider = sdklog.NewLoggerProvider(
					sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
					sdklog.WithResource(res),
				)
				global.SetLoggerProvider(logProvider)
			}
		default: // grpc
			lOpts := []otlploggrpc.Option{}
			if lgEP != "" {
				lOpts = append(lOpts, otlploggrpc.WithEndpoint(lgEP))
			}
			if lgInsecure {
				lOpts = append(lOpts, otlploggrpc.WithInsecure())
			}
			if len(lgHeaders) > 0 {
				lOpts = append(lOpts, otlploggrpc.WithHeaders(lgHeaders))
			}
			if logExp, err := otlploggrpc.New(ctx, lOpts...); err == nil {
				logProvider = sdklog.NewLoggerProvider(
					sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
					sdklog.WithResource(res),
				)
				global.SetLoggerProvider(logProvider)
			}
		}
	}

	shutdownFn = func(c context.Context) error {
		// Try trace provider shutdown first
		var err1 error
		if tp != nil {
			err1 = tp.Shutdown(c)
		}
		var err2 error
		if logProvider != nil {
			err2 = logProvider.Shutdown(c)
		}
		if err1 != nil {
			return err1
		}
		return err2
	}
	return shutdownFn, nil
}

// Shutdown flushes traces. Safe to call multiple times.
func Shutdown(ctx context.Context) error {
	if shutdownFn == nil {
		return nil
	}
	return shutdownFn(ctx)
}

// Attrs helper to build attributes succinctly.
func Attrs(kv ...string) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, attribute.String(kv[i], kv[i+1]))
	}
	return out
}

// MergeAttrs merges attributes into a single slice.
func MergeAttrs(a, b []attribute.KeyValue) []attribute.KeyValue { return append(a, b...) }

// LogProviderReady indicates if the OTLP log exporter/provider were initialized.
func LogProviderReady() bool { return logProvider != nil }

// OtelSlogHandler returns a slog.Handler that forwards records to OTel Logs.
// Returns nil if the log provider is not initialized.
func OtelSlogHandler(name string) slog.Handler {
	if logProvider == nil {
		return nil
	}
	return otelslog.NewHandler(name, otelslog.WithLoggerProvider(logProvider))
}

// resolveEndpoint determines the endpoint and insecurity for a signal ("traces" or "logs").
// Precedence: OTEL_EXPORTER_OTLP_{SIGNAL}_ENDPOINT > OTEL_EXPORTER_OTLP_ENDPOINT.
// Accepts endpoints with or ohne Schema; strips scheme and path for gRPC exporters.
func resolveEndpoint(signal, protocol string) (endpoint string, insecure bool, path string) {
	get := func(keys ...string) string {
		for _, k := range keys {
			v := strings.TrimSpace(os.Getenv(k))
			if v != "" {
				return v
			}
		}
		return ""
	}
	// Endpoint resolution
	switch signal {
	case "traces":
		endpoint = get("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
		insecure = strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_INSECURE")), "true") ||
			strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")), "true")
	case "logs":
		endpoint = get("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
		insecure = strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE")), "true") ||
			strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")), "true")
	}

	if endpoint == "" {
		// Fall back to commonly used default for local dev if general endpoint provided elsewhere
		if ge := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); ge != "" {
			endpoint = ge
		}
	}

	// Normalize depending on protocol
	switch strings.ToLower(protocol) {
	case "http", "http/protobuf":
		// Keep scheme and optional path; if missing scheme, infer from insecure flag.
		if strings.Contains(endpoint, "://") {
			if u, err := url.Parse(endpoint); err == nil {
				if u.Scheme == "http" {
					insecure = true
				}
				// default port 4318 if none was given
				host := u.Host
				if !strings.Contains(host, ":") {
					if u.Scheme == "http" {
						host += ":4318"
					} else if u.Scheme == "https" {
						host += ":4318"
					}
				}
				// Preserve full string; exporter options expect host and separate path
				endpoint = u.Scheme + "://" + host + u.Path
				path = u.Path
			}
		} else {
			// add default port if missing
			host := endpoint
			if host != "" && !strings.Contains(host, ":") {
				host += ":4318"
			}
			scheme := "https"
			if insecure {
				scheme = "http"
			}
			endpoint = scheme + "://" + host
		}
		return endpoint, insecure, path
	default: // grpc
		// Strip URL scheme/path if present
		if strings.Contains(endpoint, "://") {
			if u, err := url.Parse(endpoint); err == nil {
				if u.Scheme == "http" {
					insecure = true
				}
				// Use host:port only for gRPC
				endpoint = u.Host
			}
		}
		// If no port, default to 4317 for gRPC
		if endpoint != "" && !strings.Contains(endpoint, ":") {
			endpoint = endpoint + ":4317"
		}
		return endpoint, insecure, ""
	}
}

// resolveProtocol returns the desired protocol for a signal (grpc | http/protobuf).
// It checks per-signal then generic OTEL_EXPORTER_OTLP_*_PROTOCOL and also infers
// HTTP if the configured endpoint contains an http(s) scheme.
func resolveProtocol(signal string) string {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return strings.ToLower(v)
			}
		}
		return ""
	}
	// explicit override
	proto := ""
	switch signal {
	case "traces":
		proto = get("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL")
	case "logs":
		proto = get("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL")
	}
	if proto == "" {
		// infer from endpoint scheme
		ep := ""
		switch signal {
		case "traces":
			ep = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
		case "logs":
			ep = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"))
		}
		if ep == "" {
			ep = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
		}
		if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
			return "http/protobuf"
		}
		return "grpc"
	}
	switch proto {
	case "http", "http/protobuf":
		return "http/protobuf"
	default:
		return "grpc"
	}
}

// resolveHeaders merges generic and per-signal headers into a map.
func resolveHeaders(signal string) map[string]string {
	toMap := func(s string) map[string]string {
		if s == "" {
			return nil
		}
		m := make(map[string]string)
		parts := strings.Split(s, ",")
		for _, p := range parts {
			if p == "" {
				continue
			}
			kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
			if len(kv) == 2 {
				m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
		return m
	}
	gen := toMap(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")))
	var spec map[string]string
	switch signal {
	case "traces":
		spec = toMap(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS")))
	case "logs":
		spec = toMap(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS")))
	}
	if len(gen) == 0 && len(spec) == 0 {
		return nil
	}
	out := make(map[string]string)
	for k, v := range gen {
		out[k] = v
	}
	for k, v := range spec {
		out[k] = v
	}
	return out
}

// parseURL splits url string into host(with port), scheme and path.
// If input is empty or invalid, returns all empty strings.
func parseURL(s string) (host, scheme, path string) {
	if s == "" {
		return "", "", ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", "", ""
	}
	return u.Host, u.Scheme, u.Path
}

// hasAnyOTLPConfig returns true if any relevant OTLP endpoint env var is set.
// Telemetry remains disabled unless an explicit endpoint is provided.
func hasAnyOTLPConfig() bool {
	keys := []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
	}
	for _, k := range keys {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

func isTracesConfigured() bool {
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" {
		return true
	}
	return false
}

func isLogsConfigured() bool {
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" {
		return true
	}
	return false
}
