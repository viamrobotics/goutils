// Package main runs a gRPC server running the proto/rpc/examples/echo/v1 service.
//
// It is accessible over gRPC, grpc-web, gRPC via RESTful JSON, and gRPC via WebRTC.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/go-errors/errors"

	"go.viam.com/utils"
	"go.viam.com/utils/internal"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc/examples/echo/server"
	rpcserver "go.viam.com/utils/rpc/server"

	"github.com/edaniels/golog"
	"go.uber.org/multierr"
	"goji.io"
	"goji.io/pat"
)

func main() {
	utils.ContextualMain(mainWithArgs, logger)
}

var (
	defaultPort = 8080
	logger      = golog.Global.Named("server")
)

// Arguments for the command.
type Arguments struct {
	Port             utils.NetPortFlag `flag:"0"`
	SignalingAddress string            `flag:"signaling_address,default="`
	SignalingHost    string            `flag:"signaling_host,default=local"`
	Insecure         bool              `flag:"insecure"`
}

func mainWithArgs(ctx context.Context, args []string, logger golog.Logger) error {
	var argsParsed Arguments
	if err := utils.ParseFlags(args, &argsParsed); err != nil {
		return err
	}
	if argsParsed.Port == 0 {
		argsParsed.Port = utils.NetPortFlag(defaultPort)
	}
	if argsParsed.SignalingAddress == "" {
		argsParsed.Insecure = true
	}

	return runServer(
		ctx,
		int(argsParsed.Port),
		argsParsed.SignalingAddress, argsParsed.SignalingHost,
		argsParsed.Insecure,
		logger,
	)
}

func runServer(
	ctx context.Context,
	port int,
	signalingAddress, signalingHost string,
	insecure bool,
	logger golog.Logger,
) (err error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return err
	}

	rpcServer, err := rpcserver.NewWithOptions(
		rpcserver.Options{WebRTC: rpcserver.WebRTCOptions{
			Enable:           true,
			EnableSignaling:  true,
			Insecure:         insecure,
			SignalingAddress: signalingAddress,
			SignalingHost:    signalingHost,
		}},
		logger,
	)
	if err != nil {
		return err
	}
	defer func() {
		err = multierr.Combine(err, rpcServer.Stop())
	}()

	if err := rpcServer.RegisterServiceServer(
		ctx,
		&echopb.EchoService_ServiceDesc,
		&server.Server{},
		echopb.RegisterEchoServiceHandlerFromEndpoint,
	); err != nil {
		return err
	}

	mux := goji.NewMux()
	mux.Handle(pat.Get("/"), http.FileServer(http.Dir(internal.ResolveFile("rpc/examples/echo/frontend/static"))))
	mux.Handle(pat.Get("/static/*"), http.StripPrefix("/static", http.FileServer(http.Dir(internal.ResolveFile("rpc/examples/echo/frontend/dist")))))
	mux.Handle(pat.New("/api/*"), http.StripPrefix("/api", rpcServer.GatewayHandler()))
	mux.Handle(pat.New("/*"), rpcServer.GRPCHandler())

	httpServer, err := utils.NewPlainTextHTTP2Server(mux)
	if err != nil {
		return err
	}
	httpServer.Addr = listener.Addr().String()

	utils.PanicCapturingGo(func() {
		<-ctx.Done()
		defer func() {
			if err := rpcServer.Stop(); err != nil {
				panic(err)
			}
		}()
		if err := httpServer.Shutdown(context.Background()); err != nil {
			panic(err)
		}
	})
	utils.PanicCapturingGo(func() {
		if err := rpcServer.Start(); err != nil {
			panic(err)
		}
	})
	utils.ContextMainReadyFunc(ctx)()

	logger.Infow("serving", "url", fmt.Sprintf("http://%s", listener.Addr().String()))
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
