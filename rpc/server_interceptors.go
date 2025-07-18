package rpc

import (
	"context"
	"path"
	"time"

	grpc_logging "github.com/grpc-ecosystem/go-grpc-middleware/logging"
	"github.com/pkg/errors"
	"go.opencensus.io/plugin/ochttp/propagation/tracecontext"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
)

// UnaryServerTracingInterceptor starts a new Span if Span metadata exists in the context.
func UnaryServerTracingInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if remoteSpanContext, err := remoteSpanContextFromContext(ctx); err == nil {
			var span *trace.Span
			println("tracing: remote span", remoteSpanContext.IsSampled(), remoteSpanContext.TraceID.String())
			ctx, span = trace.StartSpanWithRemoteParent(ctx, "server_root", remoteSpanContext)
			println("tracing: local span", span.SpanContext().IsSampled())
			defer span.End()
		} else {
			println("tracing: err getting remote span", err.Error())
		}

		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if _, ok := status.FromError(err); ok {
			return resp, err
		}
		if s := status.FromContextError(err); s != nil {
			return resp, s.Err()
		}
		return nil, err
	}
}

// StreamServerTracingInterceptor starts a new Span if Span metadata exists in the context.
func StreamServerTracingInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if remoteSpanContext, err := remoteSpanContextFromContext(stream.Context()); err == nil {
			newCtx, span := trace.StartSpanWithRemoteParent(stream.Context(), "server_root", remoteSpanContext)
			defer span.End()
			stream = wrapServerStream(newCtx, stream)
		}

		err := handler(srv, stream)
		if err == nil {
			return nil
		}
		if _, ok := status.FromError(err); ok {
			return err
		}
		if s := status.FromContextError(err); s != nil {
			return s.Err()
		}
		return err
	}
}

type serverStreamWrapper struct {
	grpc.ServerStream
	ctx context.Context
}

// Context returns the context for this stream.
func (s *serverStreamWrapper) Context() context.Context {
	return s.ctx
}

func wrapServerStream(ctx context.Context, stream grpc.ServerStream) *serverStreamWrapper {
	s := serverStreamWrapper{ServerStream: stream, ctx: ctx}
	return &s
}

func remoteSpanContextFromContext(ctx context.Context) (trace.SpanContext, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return trace.SpanContext{}, errors.New("no metadata in context")
	}

	// see https://cloud.google.com/trace/docs/trace-context
	traceparent := md.Get("traceparent")
	if len(traceparent) == 0 {
		return trace.SpanContext{}, errors.New("no traceparent header")
	}

	format := tracecontext.HTTPFormat{}
	spanContext, ok := format.SpanContextFromHeaders(traceparent[0], "")
	if !ok {
		return trace.SpanContext{}, errors.New("error parsing traceparent")
	}
	return spanContext, nil
}

func grpcUnaryServerInterceptor(logger utils.ZapCompatibleLogger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		startTime := time.Now()
		resp, err := handler(ctx, req)
		code := grpc_logging.DefaultErrorToCode(err)
		loggerWithFields := utils.AddFieldsToLogger(logger, serverCallFields(ctx, info.FullMethod, startTime)...)

		utils.LogFinalLine(loggerWithFields, startTime, err, "finished unary call with code "+code.String(), code)

		return resp, err
	}
}

func grpcStreamServerInterceptor(logger utils.ZapCompatibleLogger) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		startTime := time.Now()
		err := handler(srv, stream)
		code := grpc_logging.DefaultErrorToCode(err)
		loggerWithFields := utils.AddFieldsToLogger(logger, serverCallFields(stream.Context(), info.FullMethod, startTime)...)

		utils.LogFinalLine(loggerWithFields, startTime, err, "finished stream call with code "+code.String(), code)

		return err
	}
}

const iso8601 = "2006-01-02T15:04:05.000Z0700" // keep timestamp formatting constant

func serverCallFields(ctx context.Context, fullMethodString string, start time.Time) []any {
	var f []any
	f = append(f, "grpc.start_time", start.UTC().Format(iso8601))
	if d, ok := ctx.Deadline(); ok {
		f = append(f, "grpc.request.deadline", d.UTC().Format(iso8601))
	}
	service := path.Dir(fullMethodString)[1:]
	method := path.Base(fullMethodString)
	return append(f, []any{
		"span.kind", "server",
		"system", "grpc",
		"grpc.service", service,
		"grpc.method", method,
	}...)
}
