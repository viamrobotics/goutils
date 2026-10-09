package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.viam.com/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestWebRTCServerStreamHeaderRace(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)
	defer utils.UncheckedErrorFunc(pc1.GracefulClose)
	defer utils.UncheckedErrorFunc(pc2.GracefulClose)

	// clientCh is not used directly in the test but the test will leak goroutines if not used.
	clientCh := newWebRTCClientChannel(pc1, dc1, nil, utils.Sublogger(logger, "client"), nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()

	server := newWebRTCServer(logger)
	defer server.Stop()

	serverCh := newWebRTCServerChannel(server, pc2, dc2, []string{"one", "two"}, "", nil, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	stream := newWebRTCServerStream(context.Background(), nil, "", serverCh, nil, nil, logger)
	defer stream.CloseRecv()

	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		stream.SendHeader(metadata.New(map[string]string{"abc": "def"}))
	}()
	go func() {
		defer wg.Done()
		stream.SetHeader(metadata.New(map[string]string{"hello": "world"}))
	}()
	wg.Wait()
}

const otelStatusCodeKey = attribute.Key("rpc.grpc.status_code")

// statsEchoServer fails Echo and EchoMultiple with codes.Unavailable when the message is "unavailable".
// For the message "loop", EchoMultiple sends until Send fails and reports that error on sendErr.
type statsEchoServer struct {
	echoserver.Server
	sendErr chan error
}

func (srv *statsEchoServer) Echo(ctx context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	if req.GetMessage() == "unavailable" {
		return nil, status.Error(codes.Unavailable, "unavailable")
	}
	return srv.Server.Echo(ctx, req)
}

func (srv *statsEchoServer) EchoMultiple(req *echopb.EchoMultipleRequest, server echopb.EchoService_EchoMultipleServer) error {
	switch req.GetMessage() {
	case "unavailable":
		return status.Error(codes.Unavailable, "unavailable")
	case "loop":
		for {
			if err := server.Send(&echopb.EchoMultipleResponse{Message: "loop"}); err != nil {
				srv.sendErr <- err
				return err
			}
		}
	default:
		return srv.Server.EchoMultiple(req, server)
	}
}

func TestWebRTCServerStreamStatsHandler(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)
	defer utils.UncheckedErrorFunc(pc1.GracefulClose)
	defer utils.UncheckedErrorFunc(pc2.GracefulClose)

	spanExporter := tracetest.NewInMemoryExporter()
	metricReader := sdkmetric.NewManualReader()
	statsHandler := otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))),
		otelgrpc.WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))),
	)

	server := newWebRTCServerWithOptions(logger, nil, nil, nil, statsHandler)
	defer server.Stop()
	echoServer := &statsEchoServer{sendErr: make(chan error, 1)}
	server.RegisterService(&echopb.EchoService_ServiceDesc, echoServer)

	clientCh := newWebRTCClientChannel(pc1, dc1, nil, utils.Sublogger(logger, "client"), nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()

	serverCh := newWebRTCServerChannel(server, pc2, dc2, []string{"one", "two"}, "", nil, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	assertServerStats := func(t *testing.T, method string, code codes.Code, spanStatus otelcodes.Code) {
		t.Helper()
		testutils.WaitForAssertion(t, func(tb testing.TB) {
			tb.Helper()
			spans := spanExporter.GetSpans()
			test.That(tb, spans, test.ShouldHaveLength, 1)
			// WaitForAssertion retries with a tb whose FailNow does not stop the goroutine.
			if len(spans) != 1 {
				return
			}
			test.That(tb, spans[0].Status.Code, test.ShouldEqual, spanStatus)
			test.That(tb, spans[0].Attributes, test.ShouldContain, otelStatusCodeKey.Int(int(code)))

			var rm metricdata.ResourceMetrics
			test.That(tb, metricReader.Collect(context.Background(), &rm), test.ShouldBeNil)
			point, ok := serverDurationPoint(rm, method, code)
			test.That(tb, ok, test.ShouldBeTrue)
			test.That(tb, point.Count, test.ShouldEqual, 1)
			test.That(tb, point.Sum, test.ShouldBeGreaterThan, 0)
		})
	}

	client := echopb.NewEchoServiceClient(clientCh)
	call := func(method, message string) error {
		if method == "Echo" {
			_, err := client.Echo(context.Background(), &echopb.EchoRequest{Message: message})
			return err
		}
		stream, err := client.EchoMultiple(context.Background(), &echopb.EchoMultipleRequest{Message: message})
		if err != nil {
			return err
		}
		for {
			if _, err := stream.Recv(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		}
	}

	for _, tc := range []struct {
		method     string
		message    string
		code       codes.Code
		spanStatus otelcodes.Code
	}{
		{"Echo", "hello", codes.OK, otelcodes.Unset},
		{"Echo", "unavailable", codes.Unavailable, otelcodes.Error},
		{"EchoMultiple", "hello", codes.OK, otelcodes.Unset},
		{"EchoMultiple", "unavailable", codes.Unavailable, otelcodes.Error},
	} {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.code), func(t *testing.T) {
			spanExporter.Reset()
			test.That(t, status.Code(call(tc.method, tc.message)), test.ShouldEqual, tc.code)
			assertServerStats(t, tc.method, tc.code, tc.spanStatus)
		})
	}

	t.Run("EchoMultiple client cancel", func(t *testing.T) {
		spanExporter.Reset()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream, err := client.EchoMultiple(ctx, &echopb.EchoMultipleRequest{Message: "loop"})
		test.That(t, err, test.ShouldBeNil)
		_, err = stream.Recv()
		test.That(t, err, test.ShouldBeNil)
		cancel()

		select {
		case err := <-echoServer.sendErr:
			test.That(t, err, test.ShouldBeError, io.ErrClosedPipe)
		case <-time.After(5 * time.Second):
			t.Fatal("handler did not observe the cancel")
		}
		assertServerStats(t, "EchoMultiple", codes.Canceled, otelcodes.Unset)
	})
}

func serverDurationPoint(rm metricdata.ResourceMetrics, method string, code codes.Code) (metricdata.HistogramDataPoint[float64], bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok || m.Name != "rpc.server.duration" {
				continue
			}
			for _, dp := range hist.DataPoints {
				gotMethod, _ := dp.Attributes.Value("rpc.method")
				gotCode, _ := dp.Attributes.Value(otelStatusCodeKey)
				if gotMethod.AsString() == method && gotCode.AsInt64() == int64(code) {
					return dp, true
				}
			}
		}
	}
	return metricdata.HistogramDataPoint[float64]{}, false
}
