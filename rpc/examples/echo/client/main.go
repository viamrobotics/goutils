// Package main runs a gRPC client over WebRTC connecting to the proto/rpc/examples/echo/v1 service.
package main

import (
	"context"
	"io"

	"github.com/edaniels/golog"
	"github.com/pkg/errors"
	"go.uber.org/multierr"

	"go.viam.com/utils"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc"
)

func main() {
	utils.ContextualMain(mainWithArgs, logger)
}

var logger = golog.Global.Named("client")

// Arguments for the command.
type Arguments struct {
	Host                string `flag:"host,default=local"`
	SignalingServer     string `flag:"signaling_server,default=localhost:8080"`
	Insecure            bool   `flag:"insecure"`
	APIKey              string `flag:"api_key"`
	ExternalAuthAddress string `flag:"external_auth_addr"`
}

func mainWithArgs(ctx context.Context, args []string, logger golog.Logger) (err error) {
	var argsParsed Arguments
	if err := utils.ParseFlags(args, &argsParsed); err != nil {
		return err
	}

	var dialOpts []rpc.DialOption
	if argsParsed.Insecure {
		dialOpts = append(dialOpts, rpc.WithInsecure())
	}
	if argsParsed.SignalingServer != "" {
		webRTCOpts := rpc.DialWebRTCOptions{
			SignalingServerAddress: argsParsed.SignalingServer,
		}
		if argsParsed.Insecure {
			webRTCOpts.SignalingInsecure = true
		}
		if argsParsed.APIKey != "" {
			webRTCOpts.SignalingCreds = rpc.Credentials{
				Type:    rpc.CredentialsTypeAPIKey,
				Payload: argsParsed.APIKey,
			}
		}
		if argsParsed.ExternalAuthAddress != "" {
			webRTCOpts.SignalingExternalAuthAddress = argsParsed.ExternalAuthAddress
			webRTCOpts.SignalingExternalAuthToEntity = argsParsed.Host
		}
		dialOpts = append(dialOpts, rpc.WithWebRTCOptions(webRTCOpts))
	}
	if argsParsed.APIKey != "" {
		dialOpts = append(dialOpts, rpc.WithCredentials(rpc.Credentials{
			Type:    rpc.CredentialsTypeAPIKey,
			Payload: argsParsed.APIKey,
		}))
	}
	if argsParsed.ExternalAuthAddress != "" {
		dialOpts = append(dialOpts, rpc.WithExternalAuth(
			argsParsed.ExternalAuthAddress,
			argsParsed.Host,
		))
	}
	cc, err := rpc.Dial(ctx, argsParsed.Host, logger, dialOpts...)
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
		return errors.WithStack(err)
	}
	logger.Infow("echo", "resp", resp.Message)

	multiClient, err := echoClient.EchoMultiple(ctx, &echopb.EchoMultipleRequest{Message: "hello?"})
	if err != nil {
		return errors.WithStack(err)
	}
	for {
		resp, err := multiClient.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return errors.WithStack(err)
			}
			break
		}
		logger.Infow("echo multi", "resp", resp.Message)
	}

	biDiClient, err := echoClient.EchoBiDi(ctx)
	if err != nil {
		return errors.WithStack(err)
	}
	if err := biDiClient.Send(&echopb.EchoBiDiRequest{Message: "one"}); err != nil {
		return errors.WithStack(err)
	}
	for i := 0; i < 3; i++ {
		resp, err := biDiClient.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return errors.WithStack(err)
			}
			break
		}
		logger.Infow("echo bidi", "resp", resp.Message)
	}

	if err := biDiClient.Send(&echopb.EchoBiDiRequest{Message: "two"}); err != nil {
		return errors.WithStack(err)
	}
	for i := 0; i < 3; i++ {
		resp, err := biDiClient.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return errors.WithStack(err)
			}
			break
		}
		logger.Infow("echo bidi", "resp", resp.Message)
	}

	if err := biDiClient.CloseSend(); err != nil {
		return errors.WithStack(err)
	}

	// Ending right here can cause server to send on a closed pipe which it
	// should handle gracefully.

	allStagesComplete = true
	return nil
}
