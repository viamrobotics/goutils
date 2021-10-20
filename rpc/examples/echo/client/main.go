// Package main runs a gRPC client over WebRTC connecting to the proto/rpc/examples/echo/v1 service.
package main

import (
	"context"
	"io"

	"github.com/edaniels/golog"
	"github.com/go-errors/errors"
	"go.uber.org/multierr"

	"go.viam.com/utils"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	rpcclient "go.viam.com/utils/rpc/client"
	rpcwebrtc "go.viam.com/utils/rpc/webrtc"
)

func main() {
	utils.ContextualMain(mainWithArgs, logger)
}

var logger = golog.Global.Named("client")

// Arguments for the command.
type Arguments struct {
	Host            string `flag:"host,default=local"`
	SignalingServer string `flag:"signaling_server,default=localhost:8080"`
	Insecure        bool   `flag:"insecure"`
}

func mainWithArgs(ctx context.Context, args []string, logger golog.Logger) (err error) {
	var argsParsed Arguments
	if err := utils.ParseFlags(args, &argsParsed); err != nil {
		return err
	}

	cc, err := rpcclient.Dial(ctx, argsParsed.Host, rpcclient.DialOptions{
		Insecure: argsParsed.Insecure,
		WebRTC: rpcwebrtc.Options{
			SignalingServer: argsParsed.SignalingServer,
		},
	}, logger)
	if err != nil {
		return err
	}
	defer func() {
		err = multierr.Combine(err, cc.Close())
	}()

	var allStagesComplete bool
	defer func() {
		if !allStagesComplete {
			err = multierr.Combine(err, errors.New("failed to complete all stages"))
		}
	}()

	echoClient := echopb.NewEchoServiceClient(cc)
	resp, err := echoClient.Echo(ctx, &echopb.EchoRequest{Message: "hello?"})
	if err != nil {
		return errors.Wrap(err, 0)
	}
	logger.Infow("echo", "resp", resp.Message)

	multiClient, err := echoClient.EchoMultiple(ctx, &echopb.EchoMultipleRequest{Message: "hello?"})
	if err != nil {
		return errors.Wrap(err, 0)
	}
	for {
		resp, err := multiClient.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return errors.Wrap(err, 0)
			}
			break
		}
		logger.Infow("echo multi", "resp", resp.Message)
	}

	biDiClient, err := echoClient.EchoBiDi(ctx)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	if err := biDiClient.Send(&echopb.EchoBiDiRequest{Message: "one"}); err != nil {
		return errors.Wrap(err, 0)
	}
	for i := 0; i < 3; i++ {
		resp, err := biDiClient.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return errors.Wrap(err, 0)
			}
			break
		}
		logger.Infow("echo bidi", "resp", resp.Message)
	}

	if err := biDiClient.Send(&echopb.EchoBiDiRequest{Message: "two"}); err != nil {
		return errors.Wrap(err, 0)
	}
	for i := 0; i < 3; i++ {
		resp, err := biDiClient.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return errors.Wrap(err, 0)
			}
			break
		}
		logger.Infow("echo bidi", "resp", resp.Message)
	}

	if err := biDiClient.CloseSend(); err != nil {
		return errors.Wrap(err, 0)
	}

	// Ending right here can cause server to send on a closed pipe which it
	// should handle gracefully.

	allStagesComplete = true
	return nil
}
