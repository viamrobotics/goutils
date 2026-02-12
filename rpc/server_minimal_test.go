package rpc

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// MinimalTestService for testing flow control
type MinimalTestService struct{}

// TestUploadRequest mimics a streaming upload request
type TestUploadRequest struct {
	Data []byte
}

func (r *TestUploadRequest) Reset()         { *r = TestUploadRequest{} }
func (r *TestUploadRequest) String() string { return fmt.Sprintf("TestUploadRequest{%d bytes}", len(r.Data)) }
func (r *TestUploadRequest) ProtoMessage()  {}

// TestUploadResponse mimics an upload response
type TestUploadResponse struct {
	BytesReceived int64
}

func (r *TestUploadResponse) Reset()         { *r = TestUploadResponse{} }
func (r *TestUploadResponse) String() string { return fmt.Sprintf("TestUploadResponse{%d bytes}", r.BytesReceived) }
func (r *TestUploadResponse) ProtoMessage()  {}

var _ proto.Message = (*TestUploadRequest)(nil)
var _ proto.Message = (*TestUploadResponse)(nil)

type MinimalTestServer struct {
	chunksReceived atomic.Int64
	bytesReceived  atomic.Int64
	startTime      time.Time
	delayMs        int
	logInterval    int64
}

func (s *MinimalTestServer) Upload(stream grpc.ServerStream) error {
	totalBytes := int64(0)
	chunks := int64(0)

	// Simulate GCS Writer buffering
	const gcsBufferSize = 16 * 1024 * 1024 // 16MB
	bufferedBytes := int64(0)

	for {
		req := &TestUploadRequest{}
		err := stream.RecvMsg(req)
		if err == io.EOF {
			resp := &TestUploadResponse{BytesReceived: totalBytes}
			return stream.SendMsg(resp)
		}
		if err != nil {
			return err
		}

		totalBytes += int64(len(req.Data))
		chunks++
		bufferedBytes += int64(len(req.Data))

		s.chunksReceived.Add(1)
		s.bytesReceived.Add(int64(len(req.Data)))

		// Simulate GCS buffer flush
		if bufferedBytes >= gcsBufferSize {
			if s.delayMs > 0 {
				log.Printf("  [GCS] Flushing %d bytes (simulating slow upload)...", bufferedBytes)
				time.Sleep(time.Duration(s.delayMs) * time.Millisecond)
			}
			bufferedBytes = 0
		}

		// Log memory stats periodically
		if chunks%s.logInterval == 0 {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			elapsed := time.Since(s.startTime).Seconds()

			log.Printf("Chunks: %d | Bytes: %.2f MB | Buffered: %.2f MB | HeapAlloc: %.2f MB | HeapInuse: %.2f MB | Rate: %.2f MB/s",
				chunks,
				float64(totalBytes)/(1024*1024),
				float64(bufferedBytes)/(1024*1024),
				float64(m.Alloc)/(1024*1024),
				float64(m.HeapInuse)/(1024*1024),
				float64(s.bytesReceived.Load())/(1024*1024*elapsed),
			)
		}
	}
}

var (
	testPort            = flag.Int("test-port", 50052, "Test server port")
	testDelay           = flag.Int("test-delay", 2000, "Delay in ms for GCS simulation")
	testUseStaticWindow = flag.Bool("test-static", false, "Use static flow control windows")
	testWindowSize      = flag.Int("test-window", 64*1024, "Flow control window size")
	testLogInterval     = flag.Int64("test-log-interval", 100, "Log every N chunks")
)

// RunMinimalTestServer creates and runs a minimal gRPC server for flow control testing
func RunMinimalTestServer() {
	flag.Parse()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *testPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	opts := []grpc.ServerOption{}

	if *testUseStaticWindow {
		log.Printf("Using STATIC flow control windows: %d bytes (BDP disabled)", *testWindowSize)
		opts = append(opts,
			grpc.StaticStreamWindowSize(int32(*testWindowSize)),
			grpc.StaticConnWindowSize(int32(*testWindowSize)),
		)
	} else {
		log.Printf("Using DYNAMIC flow control windows (BDP enabled, will grow to 16MB)")
		opts = append(opts,
			grpc.InitialWindowSize(int32(*testWindowSize)),
			grpc.InitialConnWindowSize(int32(*testWindowSize)),
		)
	}

	opts = append(opts,
		grpc.MaxConcurrentStreams(100),
		grpc.WriteBufferSize(0),
		grpc.ReadBufferSize(64*1024),
	)

	s := grpc.NewServer(opts...)

	// Register the minimal test service
	testServer := &MinimalTestServer{
		startTime:   time.Now(),
		delayMs:     *testDelay,
		logInterval: *testLogInterval,
	}

	serviceDesc := &grpc.ServiceDesc{
		ServiceName: "test.MinimalTestService",
		HandlerType: (*MinimalTestService)(nil),
		Methods:     []grpc.MethodDesc{},
		Streams: []grpc.StreamDesc{
			{
				StreamName:    "Upload",
				Handler:       testServer.Upload,
				ServerStreams: false,
				ClientStreams: true,
			},
		},
		Metadata: "minimal_test.proto",
	}

	s.RegisterService(serviceDesc, testServer)

	log.Printf("Minimal test server starting on port %d", *testPort)
	log.Printf("Configuration: delay=%dms, log_interval=%d", *testDelay, *testLogInterval)

	// Signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Memory monitor
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				elapsed := time.Since(testServer.startTime).Seconds()

				log.Printf("[MONITOR] Chunks: %d | Bytes: %.2f MB | HeapAlloc: %.2f MB | HeapInuse: %.2f MB | Rate: %.2f MB/s",
					testServer.chunksReceived.Load(),
					float64(testServer.bytesReceived.Load())/(1024*1024),
					float64(m.Alloc)/(1024*1024),
					float64(m.HeapInuse)/(1024*1024),
					float64(testServer.bytesReceived.Load())/(1024*1024*elapsed),
				)
			}
		}
	}()

	// Start server
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Printf("Server stopped: %v", err)
		}
	}()

	log.Printf("Server running. Press Ctrl+C to stop.")

	// Wait for signal
	<-sigCh
	log.Println("\nReceived interrupt signal, shutting down gracefully...")
	cancel()
	s.GracefulStop()
	log.Println("Server stopped.")
}
