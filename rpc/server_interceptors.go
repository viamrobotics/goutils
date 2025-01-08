package rpc

import (
	"context"
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"go.opencensus.io/trace"
	"go.viam.com/utils"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_logging "github.com/grpc-ecosystem/go-grpc-middleware/logging"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryServerTracingInterceptor starts a new Span if Span metadata exists in the context.
func UnaryServerTracingInterceptor(logger utils.ZapCompatibleLogger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if remoteSpanContext, err := remoteSpanContextFromContext(ctx); err == nil {
			var span *trace.Span
			ctx, span = trace.StartSpanWithRemoteParent(ctx, "server_root", remoteSpanContext)
			defer span.End()
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
func StreamServerTracingInterceptor(logger utils.ZapCompatibleLogger) grpc.StreamServerInterceptor {
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
	var err error

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return trace.SpanContext{}, errors.New("no metadata in context")
	}

	// Extract trace-id
	traceIDMetadata := md.Get("trace-id")
	if len(traceIDMetadata) == 0 {
		return trace.SpanContext{}, errors.New("trace-id is missing from metadata")
	}

	traceIDBytes, err := hex.DecodeString(traceIDMetadata[0])
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("trace-id could not be decoded: %w", err)
	}
	var traceID trace.TraceID
	copy(traceID[:], traceIDBytes)

	// Extract span-id
	spanIDMetadata := md.Get("span-id")
	spanIDBytes, err := hex.DecodeString(spanIDMetadata[0])
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("span-id could not be decoded: %w", err)
	}
	var spanID trace.SpanID
	copy(spanID[:], spanIDBytes)

	// Extract trace-options
	traceOptionsMetadata := md.Get("trace-options")
	if len(traceOptionsMetadata) == 0 {
		return trace.SpanContext{}, errors.New("trace-options is missing from metadata")
	}

	traceOptionsUint, err := strconv.ParseUint(traceOptionsMetadata[0], 10 /* base 10 */, 32 /* 32-bit */)
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("trace-options could not be parsed as uint: %w", err)
	}
	traceOptions := trace.TraceOptions(traceOptionsUint)

	return trace.SpanContext{TraceID: traceID, SpanID: spanID, TraceOptions: traceOptions, Tracestate: nil}, nil
}

// UnaryServerInterceptor returns a new unary server interceptors that adds zap.Logger to the context.
func grpcUnaryServerInterceptor(logger utils.ZapCompatibleLogger, opts ...grpcZapOption) grpc.UnaryServerInterceptor {
	o := evaluateServerOpt(opts)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		startTime := time.Now()

		newCtx := newLoggerForCall(ctx, logger, info.FullMethod, startTime)

		resp, err := handler(newCtx, req)
		if !o.shouldLog(info.FullMethod, err) {
			return resp, err
		}

		code := grpc_logging.DefaultErrorToCode(err)
		level := grpc_zap.DefaultCodeToLevel(code)
		// this calculation is done because duration.Milliseconds() will return an integer, which is not precise enough.
		duration := float32(time.Since(startTime).Nanoseconds()/1000) / 1000
		fields := []any{}
		if err == nil {
			level = zap.DebugLevel
		} else {
			fields = append(fields, "error", err)
		}
		fields = append(fields, "grpc.code", code.String(), "grpc.time_ms", duration)
		msg := "finished unary call with code " + code.String()

		// grpc_zap.DefaultCodeToLevel will only return zap.DebugLevel, zap.InfoLevel, zap.ErrorLevel, zap.WarnLevel
		switch level {
		case zap.DebugLevel:
			logger.Debugw(msg, fields...)
		case zap.InfoLevel:
			logger.Infow(msg, fields...)
		case zap.ErrorLevel:
			logger.Errorw(msg, fields...)
		case zap.WarnLevel, zap.DPanicLevel, zap.PanicLevel, zap.FatalLevel, zapcore.InvalidLevel:
			logger.Warnw(msg, fields...)
		}

		return resp, err
	}
}

func grpcStreamServerInterceptor(logger utils.ZapCompatibleLogger, opts ...grpcZapOption) grpc.StreamServerInterceptor {
	o := evaluateServerOpt(opts)
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		startTime := time.Now()
		newCtx := newLoggerForCall(stream.Context(), logger, info.FullMethod, startTime)
		wrapped := grpc_middleware.WrapServerStream(stream)
		wrapped.WrappedContext = newCtx

		err := handler(srv, wrapped)
		if !o.shouldLog(info.FullMethod, err) {
			return err
		}
		code := grpc_logging.DefaultErrorToCode(err)
		level := grpc_zap.DefaultCodeToLevel(code)
		// this calculation is done because duration.Milliseconds() will return an integer, which is not precise enough.
		duration := float32(time.Since(startTime).Nanoseconds()/1000) / 1000
		fields := []any{}
		if err == nil {
			level = zap.DebugLevel
		} else {
			fields = append(fields, "error", err)
		}
		fields = append(fields, "grpc.code", code.String(), "grpc.time_ms", duration)
		msg := "finished unary call with code " + code.String()

		// grpc_zap.DefaultCodeToLevel will only return zap.DebugLevel, zap.InfoLevel, zap.ErrorLevel, zap.WarnLevel
		switch level {
		case zap.DebugLevel:
			logger.Debugw(msg, fields...)
		case zap.InfoLevel:
			logger.Infow(msg, fields...)
		case zap.ErrorLevel:
			logger.Errorw(msg, fields...)
		case zap.WarnLevel, zap.DPanicLevel, zap.PanicLevel, zap.FatalLevel, zapcore.InvalidLevel:
			logger.Warnw(msg, fields...)
		}

		return err
	}
}

var (
	defaultOptions = &grpcZapOptions{
		levelFunc:    grpc_zap.DefaultCodeToLevel,
		shouldLog:    grpc_logging.DefaultDeciderMethod,
		codeFunc:     grpc_logging.DefaultErrorToCode,
		durationFunc: grpc_zap.DefaultDurationToField,
		messageFunc:  grpc_zap.DefaultMessageProducer,
	}
)

func evaluateServerOpt(opts []grpcZapOption) *grpcZapOptions {
	optCopy := &grpcZapOptions{}
	*optCopy = *defaultOptions
	optCopy.levelFunc = grpc_zap.DefaultCodeToLevel
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

func newLoggerForCall(ctx context.Context, logger utils.ZapCompatibleLogger, fullMethodString string, start time.Time) context.Context {
	var f []any
	f = append(f, "grpc.start_time", start.Format(time.RFC3339))
	if d, ok := ctx.Deadline(); ok {
		f = append(f, zap.String("grpc.request.deadline", d.Format(time.RFC3339)))
	}
	callLog := utils.AddFieldsToLogger(logger, append(f, serverCallFields(fullMethodString)...)...)
	return toContext(ctx, callLog)
}

func serverCallFields(fullMethodString string) []any {
	service := path.Dir(fullMethodString)[1:]
	method := path.Base(fullMethodString)
	return []any{
		"span.kind", "server",
		"system", "grpc",
		"grpc.service", service,
		"grpc.method", method,
	}
}

type grpcZapOptions struct {
	levelFunc    grpc_zap.CodeToLevel
	shouldLog    grpc_logging.Decider
	codeFunc     grpc_logging.ErrorToCode
	durationFunc grpc_zap.DurationToField
	messageFunc  grpc_zap.MessageProducer
}

type grpcZapOption func(*grpcZapOptions)

// ToContext adds the zap.Logger to the context for extraction later.
// Returning the new context that has been created.
func toContext(ctx context.Context, logger utils.ZapCompatibleLogger) context.Context {
	l := &ctxLogger{
		logger: logger,
	}
	return context.WithValue(ctx, ctxMarkerKey, l)
}

type ctxLogger struct {
	logger utils.ZapCompatibleLogger
	fields []any
}

var (
	ctxMarkerKey = &ctxMarker{}
	nullLogger   = zap.NewNop()
)

type ctxMarker struct{}
