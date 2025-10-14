package perf

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"testing"

	"github.com/edaniels/golog"
	"go.opencensus.io/metric/metricexport"
	"go.opencensus.io/trace"
	"goji.io"
	"goji.io/pat"

	"go.viam.com/test"
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

func TestWalkData(t *testing.T) {
	wd := &walkData{}
	path := wd.get([]string{}, "first")
	test.That(t, path.spanChain, test.ShouldResemble, []string{"first"})
	test.That(t, wd.paths, test.ShouldHaveLength, 1)

	path = wd.get([]string{}, "first")
	test.That(t, path.spanChain, test.ShouldResemble, []string{"first"})
	test.That(t, wd.paths, test.ShouldHaveLength, 1)

	path = wd.get([]string{"first"}, "second")
	test.That(t, path.spanChain, test.ShouldResemble, []string{"first", "second"})
	test.That(t, wd.paths, test.ShouldHaveLength, 2)

	path = wd.get([]string{"first"}, "second")
	test.That(t, path.spanChain, test.ShouldResemble, []string{"first", "second"})
	test.That(t, wd.paths, test.ShouldHaveLength, 2)
}

func TestTimingReport(t *testing.T) {
	outputBuffer := bytes.NewBuffer(nil)
	devExporter := &developmentExporter{
		children:       map[string][]mySpanInfo{},
		reader:         metricexport.NewReader(),
		deleteDisabled: true,
		outputWriter:   outputBuffer,
	}

	devExporter.Start()
	defer devExporter.Stop()

	// Construct a call sequence of: A calls B calls C. As well as A calls C directly. Assert that
	// there two "span paths" to C.
	ctx := context.Background()
	ctxA, spanA := trace.StartSpan(ctx, "A")
	ctxAB, spanAB := trace.StartSpan(ctxA, "B")
	_, spanABC := trace.StartSpan(ctxAB, "C")
	spanABC.End()
	spanAB.End()

	// Repeat a second call to B as well as C.
	ctxAB, spanAB = trace.StartSpan(ctxA, "B")
	_, spanABC = trace.StartSpan(ctxAB, "C")
	spanABC.End()
	spanAB.End()

	// Have A call C directly.
	_, spanAC := trace.StartSpan(ctxA, "C")
	spanAC.End()
	spanA.End()

	// fmt.Println(outputBuffer.String()) -- We could (easily) assert on the text output if it
	// weren't for timing data.

	wd := walkData{}
	devExporter.recurse(&mySpanInfo{"A", spanA.SpanContext().SpanID.String(), &trace.SpanData{
		Name: "A",
	}}, []string{}, &wd)

	// The "walk data" paths must be in call order. Depend on that for assertions.
	test.That(t, wd.paths, test.ShouldHaveLength, 4)
	test.That(t, wd.paths[0].spanChain, test.ShouldResemble, []string{"A"})
	test.That(t, wd.paths[0].count, test.ShouldEqual, 1)

	test.That(t, wd.paths[1].spanChain, test.ShouldResemble, []string{"A", "B"})
	test.That(t, wd.paths[2].count, test.ShouldEqual, 2)

	test.That(t, wd.paths[2].spanChain, test.ShouldResemble, []string{"A", "B", "C"})
	test.That(t, wd.paths[2].count, test.ShouldEqual, 2)

	test.That(t, wd.paths[3].spanChain, test.ShouldResemble, []string{"A", "C"})
	test.That(t, wd.paths[3].count, test.ShouldEqual, 1)
}
