package perf

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/edaniels/golog"
	"goji.io"
	"goji.io/pat"

	"go.viam.com/utils/rpc"
)

// ExampleNewGrpcStatsHandler shows how to create a new gRPC server with intrumentation for metrics/spans.
//
//nolint:testableexamples
func ExampleNewGrpcStatsHandler() {
	logger := golog.NewDevelopmentLogger("perf-example")

	// Create a new perf.Exporter that collects metrics/spans and exports them to the correct backend.
	// For development we log to console but in production use `perf.NewCloudExporter()` to send them
	// to GCP Cloud Monitoring (Stackdriver).
	exporter := NewDevelopmentExporter()

	// Create a gRPC server that is instrumented with metrics/spans collection.
	serverOpts := []rpc.ServerOption{
		rpc.WithDebug(),
		// Add the stats handler to the gRPC server. Will capture rpc request counts, latency, bytes received.
		// See further documentation here: https://opencensus.io/guides/grpc/go/
		rpc.WithStatsHandler(NewGrpcStatsHandler()),
	}
	grpcServer, err := rpc.NewServer(logger, serverOpts...)
	if err != nil {
		logger.Panic("failed to start grpc server")
	}

	// Start the exporting of metrics and spans.
	exporter.Start()
	// Don't forget to stop the collection once finished. Stop() will flush any pending metrics/spans.
	defer exporter.Stop()

	grpcServer.Stop()
}

// ExampleWrapHTTPHandlerForStats shows how to create a new HTTP server with intrumentation for metrics/spans.
//
//nolint:testableexamples
func ExampleWrapHTTPHandlerForStats() {
	ctx := context.Background()

	// Create a new perf.Exporter that collects metrics/spans and exports them to the correct backend.
	// For development we log to console but in production use `perf.NewCloudExporter()` to send them
	// to GCP Cloud Monitoring (Stackdriver).
	exporter := NewDevelopmentExporter()

	// Create a HTTP server that is instrumented with metrics/spans collection.
	httpServerExitDone := &sync.WaitGroup{}
	mux := goji.NewMux()
	mux.HandleFunc(pat.Get("/hello/:name"), func(w http.ResponseWriter, r *http.Request) {
		name := pat.Param(r, "name")
		fmt.Fprintf(w, "Hello, %s!", name)
	})
	srv := &http.Server{
		Addr: "localhost:0",
		// To instrument the HTTP server wrap the handler with `perf.WrapHTTPHandlerForStats()`.
		Handler: WrapHTTPHandlerForStats(mux),
	}
	go func() {
		defer httpServerExitDone.Done() // let main know we are done cleaning up

		// always returns error. ErrServerClosed on graceful close
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			// unexpected error. port in use?
			log.Fatalf("ListenAndServe(): %v", err)
		}
	}()

	if err := srv.Shutdown(ctx); err != nil {
		panic(err) // failure/timeout shutting down the server gracefully
	}
	// wait for goroutine started in startHttpServer() to stop
	httpServerExitDone.Wait()

	// Start the exporting of metrics and spans.
	exporter.Start()
	// Don't forget to stop the collection once finished. Stop() will flush any pending metrics/spans.
	defer exporter.Stop()
}

// ExampleNewRoundTripperWithStats shows how to instrument a new HTTP client with metrics.
//
//nolint:testableexamples
func ExampleNewRoundTripperWithStats() {
	logger := golog.NewDevelopmentLogger("perf-example")
	client := &http.Client{
		// Instrument the HTTP client with a new RoundTripper Transport that records the metrics/spans
		Transport: NewRoundTripperWithStats(),
	}

	res, err := client.Get("http://localhost:1234")
	if err != nil {
		logger.Panic("failed to start grpc server")
	}
	defer res.Body.Close()
}
