// Package main runs a gRPC server running the proto/rpc/examples/echo/v1 service.
//
// It is accessible over gRPC, grpc-web, gRPC via RESTful JSON, and gRPC via WebRTC.
package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"

	"github.com/Masterminds/sprig"
	"github.com/go-errors/errors"

	"go.viam.com/utils"
	"go.viam.com/utils/internal"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc"
	"go.viam.com/utils/rpc/examples/echo/server"

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
	Port               utils.NetPortFlag `flag:"0"`
	SignalingAddress   string            `flag:"signaling_address,default="`
	SignalingHost      string            `flag:"signaling_host,default=local"`
	TLSCertFile        string            `flag:"tls_cert"`
	TLSKeyFile         string            `flag:"tls_key"`
	AuthPrivateKeyFile string            `flag:"auth_private_key"`
	AuthPublicKeyFile  string            `flag:"auth_public_key"`
	APIKey             string            `flag:"api_key"`
	ExternalAuthAddr   string            `flag:"external_auth_addr"`
}

func mainWithArgs(ctx context.Context, args []string, logger golog.Logger) error {
	var argsParsed Arguments
	if err := utils.ParseFlags(args, &argsParsed); err != nil {
		return err
	}
	if argsParsed.Port == 0 {
		argsParsed.Port = utils.NetPortFlag(defaultPort)
	}
	if (argsParsed.TLSCertFile == "") != (argsParsed.TLSKeyFile == "") {
		return errors.New("must provide both tls_cert and tls_key")
	}
	if (argsParsed.AuthPrivateKeyFile != "") && (argsParsed.AuthPublicKeyFile != "") {
		return errors.New("must provide only either auth private or public key")
	}

	return runServer(
		ctx,
		int(argsParsed.Port),
		argsParsed.SignalingAddress,
		argsParsed.SignalingHost,
		argsParsed.TLSCertFile,
		argsParsed.TLSKeyFile,
		argsParsed.AuthPrivateKeyFile,
		argsParsed.AuthPublicKeyFile,
		argsParsed.APIKey,
		argsParsed.ExternalAuthAddr,
		logger,
	)
}

func runServer(
	ctx context.Context,
	port int,
	signalingAddress string,
	signalingHost string,
	tlsCertFile string,
	tlsKeyFile string,
	authPrivateKeyFile string,
	authPublicKeyFile string,
	apiKey string,
	externalAuthAddr string,
	logger golog.Logger,
) (err error) {
	var serverOpts []rpc.ServerOption
	if authPrivateKeyFile != "" {
		rd, err := ioutil.ReadFile(authPrivateKeyFile)
		if err != nil {
			return err
		}
		block, _ := pem.Decode(rd)
		authPrivateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return err
		}
		serverOpts = append(serverOpts, rpc.WithAuthRSAPrivateKey(authPrivateKey.(*rsa.PrivateKey)))
	}
	var authPublicKey *rsa.PublicKey
	if authPublicKeyFile != "" {
		rd, err := ioutil.ReadFile(authPublicKeyFile)
		if err != nil {
			return err
		}
		block, _ := pem.Decode(rd)
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return err
		}
		authPublicKey = key.(*rsa.PublicKey)
	}

	listener, secure, err := utils.NewPossiblySecureTCPListenerFromFile(port, tlsCertFile, tlsKeyFile)
	if err != nil {
		return err
	}
	var signalingOpts []rpc.DialOption
	if signalingAddress == "" && !secure {
		signalingOpts = append(signalingOpts, rpc.WithInsecure())
	}
	serverOpts = append(serverOpts, rpc.WithWebRTCServerOptions(rpc.WebRTCServerOptions{
		Enable:                    true,
		ExternalSignalingDialOpts: signalingOpts,
		ExternalSignalingAddress:  signalingAddress,
		SignalingHost:             signalingHost,
	}))
	humanAddress := fmt.Sprintf("localhost:%d", port)

	if apiKey == "" && authPublicKey == nil {
		serverOpts = append(serverOpts, rpc.WithUnauthenticated())
	} else {
		handler := rpc.MakeSimpleAuthHandler(
			[]string{
				signalingHost,
				listener.Addr().String(),
				humanAddress,
			},
			apiKey,
		)
		if authPublicKey != nil {
			handler = rpc.WithPublicKeyProvider(handler, authPublicKey)
		}

		serverOpts = append(serverOpts, rpc.WithAuthHandler(rpc.CredentialsTypeAPIKey, handler))
	}

	rpcServer, err := rpc.NewServer(logger, serverOpts...)
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

	t := template.New("foo").Funcs(template.FuncMap{
		"jsSafe": func(js string) template.JS {
			return template.JS(js)
		},
		"htmlSafe": func(html string) template.HTML {
			return template.HTML(html)
		},
	}).Funcs(sprig.FuncMap())
	t, err = t.ParseGlob(fmt.Sprintf("%s/*.html", internal.ResolveFile("rpc/examples/echo/server/templates")))
	if err != nil {
		return err
	}
	indexT := t.Lookup("index.html")

	mux := goji.NewMux()
	mux.Handle(pat.Get("/"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type Temp struct {
			ExternalAuthAddr string
			Credentials      map[string]interface{}
		}
		temp := Temp{
			ExternalAuthAddr: externalAuthAddr,
		}
		if apiKey != "" {
			temp.Credentials = map[string]interface{}{
				"type":    string(rpc.CredentialsTypeAPIKey),
				"payload": apiKey,
			}
		}

		if err := indexT.Execute(w, temp); err != nil {
			panic(err)
		}
	}))
	mux.Handle(pat.Get("/static/*"), http.StripPrefix("/static", http.FileServer(http.Dir(internal.ResolveFile("rpc/examples/echo/frontend/dist")))))
	mux.Handle(pat.New("/api/*"), http.StripPrefix("/api", rpcServer.GatewayHandler()))
	mux.Handle(pat.New("/*"), rpcServer.GRPCHandler())

	httpServer, err := utils.NewPlainTextHTTP2Server(mux)
	if err != nil {
		return err
	}
	httpServer.Addr = listener.Addr().String()

	done := make(chan struct{})
	defer func() { <-done }()
	utils.PanicCapturingGo(func() {
		defer close(done)
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

	var scheme string
	if secure {
		scheme = "https"
	} else {
		scheme = "http"
	}
	logger.Infow("serving", "url", fmt.Sprintf("%s://%s", scheme, humanAddress))
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
