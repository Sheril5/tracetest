/*
 * TraceTest
 *
 * OpenAPI definition for TraceTest endpoint and resources
 *
 * API version: 0.0.1
 * Generated by: OpenAPI Generator (https://openapi-generator.tech)
 */

package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	openapi "github.com/kubeshop/tracetest/server/go"
	"github.com/kubeshop/tracetest/server/go/executor"
	"github.com/kubeshop/tracetest/server/go/testdb"
	"github.com/kubeshop/tracetest/server/go/tracedb"
	"github.com/kubeshop/tracetest/server/go/tracedb/jaegerdb"
	"github.com/kubeshop/tracetest/server/go/tracedb/tempodb"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

var cfg = flag.String("config", "config.yaml", "path to the config file")

func main() {
	flag.Parse()
	c, err := LoadConfig(*cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	tp := initOtelTracing(ctx)
	defer func() { _ = tp.Shutdown(ctx) }()

	testDB, err := testdb.New(c.PostgresConnString)
	if err != nil {
		log.Fatal(err)
	}

	var traceDB tracedb.TraceDB
	switch {
	case c.JaegerConnectionConfig != nil:
		log.Printf("connecting to Jaeger: %s\n", c.JaegerConnectionConfig.Endpoint)
		traceDB, err = jaegerdb.New(c.JaegerConnectionConfig)
		if err != nil {
			log.Fatal(err)
		}
	case c.TempoConnectionConfig != nil:
		log.Printf("connecting to tempo: %s\n", c.TempoConnectionConfig.Endpoint)
		traceDB, err = tempodb.New(c.TempoConnectionConfig)
		if err != nil {
			log.Fatal(err)
		}
	}

	ex, err := executor.New()
	if err != nil {
		log.Fatal(err)
	}

	maxWaitTimeForTrace, err := time.ParseDuration(c.MaxWaitTimeForTrace)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("maxWait", maxWaitTimeForTrace)

	apiApiService := openapi.NewApiApiService(traceDB, testDB, ex, maxWaitTimeForTrace)
	apiApiController := openapi.NewApiApiController(apiApiService)

	router := openapi.NewRouter(apiApiController)
	router.Use(otelmux.Middleware("tracetest"))
	dir := "./html"
	router.PathPrefix("/").Handler(http.FileServer(http.Dir(dir)))

	log.Printf("Server started")
	log.Fatal(http.ListenAndServe(":8080", router))
}

func initOtelTracing(ctx context.Context) *sdktrace.TracerProvider {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	var (
		exporter sdktrace.SpanExporter
		err      error
	)

	if endpoint == "" {
		endpoint = "opentelemetry-collector:4317"
		exporter, err = stdouttrace.New(stdouttrace.WithWriter(io.Discard))
		if err != nil {
			log.Fatal(err)
		}
	} else {
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		}
		exporter, err = otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
		if err != nil {
			log.Fatal(err)
		}
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{}))

	// Set standard attributes per semantic conventions
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("tracetest"),
	)

	// Create and set the TraceProvider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp
}
