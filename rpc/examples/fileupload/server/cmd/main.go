// Package main runs a gRPC server running the proto/rpc/examples/fileupload/v1 service.
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
	"github.com/edaniels/golog"
	"github.com/pkg/errors"
	"go.uber.org/multierr"
	"goji.io"
	"goji.io/pat"

	"go.viam.com/utils"
	"go.viam.com/utils/internal"
	fupb "go.viam.com/utils/proto/rpc/examples/fileupload/v1"
	"go.viam.com/utils/rpc"
	"go.viam.com/utils/rpc/examples/fileupload/server"
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
	ExternalAuth       bool              `flag:"external_auth"`
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
		argsParsed.ExternalAuth,
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
	externalAuth bool,
	logger golog.Logger,
) (err error) {
	var serverOpts []rpc.ServerOption
	var authPrivKey *rsa.PrivateKey
	if authPrivateKeyFile != "" {
		//nolint:gosec
		rd, err := ioutil.ReadFile(authPrivateKeyFile)
		if err != nil {
			return err
		}
		block, _ := pem.Decode(rd)
		authPrivateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return err
		}
		var ok bool
		authPrivKey, ok = authPrivateKey.(*rsa.PrivateKey)
		if !ok {
			return errors.Errorf("expected private key to be RSA but got %T", authPrivateKey)
		}
		serverOpts = append(serverOpts, rpc.WithAuthRSAPrivateKey(authPrivKey))
	}
	var authPublicKey *rsa.PublicKey
	if authPublicKeyFile != "" {
		//nolint:gosec
		rd, err := ioutil.ReadFile(authPublicKeyFile)
		if err != nil {
			return err
		}
		block, _ := pem.Decode(rd)
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return err
		}
		var ok bool
		authPublicKey, ok = key.(*rsa.PublicKey)
		if !ok {
			return errors.Errorf("expected *rsa.PublicKey but got %T", key)
		}
	}

	bindAddress := fmt.Sprintf("localhost:%d", port)
	listener, secure, err := utils.NewPossiblySecureTCPListenerFromFile(bindAddress, tlsCertFile, tlsKeyFile)
	if err != nil {
		return err
	}
	listenerAddr := listener.Addr().String()
	var signalingOpts []rpc.DialOption
	if signalingAddress != "" && !secure {
		signalingOpts = append(signalingOpts, rpc.WithInsecure())
	}
	serverOpts = append(serverOpts, rpc.WithWebRTCServerOptions(rpc.WebRTCServerOptions{
		Enable:                    true,
		ExternalSignalingDialOpts: signalingOpts,
		ExternalSignalingAddress:  signalingAddress,
		SignalingHosts:            []string{signalingHost},
	}))

	if apiKey == "" && authPublicKey == nil {
		serverOpts = append(serverOpts, rpc.WithUnauthenticated())
	} else {
		authEntities := []string{
			signalingHost,
			listenerAddr,
			bindAddress,
		}
		handler := rpc.MakeSimpleAuthHandler(authEntities, apiKey)
		serverOpts = append(serverOpts, rpc.WithAuthHandler(rpc.CredentialsTypeAPIKey, handler))

		if authPublicKey != nil {
			serverOpts = append(serverOpts, rpc.WithAuthHandler("inter-node", rpc.WithPublicKeyProvider(
				rpc.MakeSimpleVerifyEntity(authEntities),
				authPublicKey,
			)))
		}
	}

	if externalAuth {
		if authPrivKey == nil {
			return errors.New("expected auth_private_key")
		}
		serverOpts = append(serverOpts, rpc.WithAuthenticateToHandler(
			rpc.CredentialsType("inter-node"),
			func(ctx context.Context, entity string) (map[string]string, error) {
				return map[string]string{}, nil
			},
		))
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
		&fupb.FileUploadService_ServiceDesc,
		&server.Server{},
		fupb.RegisterFileUploadServiceHandlerFromEndpoint,
	); err != nil {
		return err
	}

	t := template.New("foo").Funcs(template.FuncMap{
		//nolint:gosec
		"jsSafe": func(js string) template.JS {
			return template.JS(js)
		},
		//nolint:gosec
		"htmlSafe": func(html string) template.HTML {
			return template.HTML(html)
		},
	}).Funcs(sprig.FuncMap())
	t, err = t.ParseGlob(fmt.Sprintf("%s/*.html", internal.ResolveFile("rpc/examples/fileupload/server/templates")))
	if err != nil {
		return err
	}
	indexT := t.Lookup("index.html")

	mux := goji.NewMux()
	mux.Handle(pat.Get("/"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type Temp struct {
			ExternalAuthAddr     string
			ExternalAuthToEntity string
			Credentials          map[string]interface{}
		}
		temp := Temp{
			ExternalAuthAddr:     externalAuthAddr,
			ExternalAuthToEntity: signalingHost,
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
	mux.Handle(pat.Get("/static/*"),
		http.StripPrefix("/static", http.FileServer(http.Dir(internal.ResolveFile("rpc/examples/fileupload/frontend/dist")))))
	mux.Handle(pat.New("/api/*"), http.StripPrefix("/api", rpcServer.GatewayHandler()))
	mux.Handle(pat.New("/*"), rpcServer.GRPCHandler())

	httpServer, err := utils.NewPlainTextHTTP2Server(mux)
	if err != nil {
		return err
	}
	httpServer.Addr = listenerAddr

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
		if err := httpServer.Shutdown(ctx); err != nil {
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
	logger.Infow("serving", "url", fmt.Sprintf("%s://%s", scheme, listenerAddr))
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
