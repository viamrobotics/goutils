// Package main runs a gRPC server running the proto/rpc/examples/echo/v1 service.
//
// It is accessible over gRPC, grpc-web, gRPC via RESTful JSON, and gRPC via WebRTC.
package main

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/Masterminds/sprig"
	"github.com/edaniels/golog"
	"github.com/golang-jwt/jwt/v4"
	"github.com/pkg/errors"
	"go.uber.org/multierr"
	"goji.io"
	"goji.io/pat"

	"go.viam.com/utils"
	"go.viam.com/utils/internal"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc"
	"go.viam.com/utils/rpc/examples/echo/server"
)

func main() {
	utils.ContextualMain(mainWithArgs, logger)
}

var (
	defaultPort = 8080
	logger      = golog.Global().Named("server")
)

// Arguments for the command.
type Arguments struct {
	Port               utils.NetPortFlag `flag:"0"`
	BindAddress        string            `flag:"bind-address"`
	InstanceName       string            `flag:"instance-name"`
	SignalingAddress   string            `flag:"signaling-address"`
	TLSCertFile        string            `flag:"tls-cert"`
	TLSKeyFile         string            `flag:"tls-key"`
	TLSAuth            bool              `flag:"tls-auth"`
	AuthPrivateKeyFile string            `flag:"auth-private-key"`
	AuthPublicKeyFile  string            `flag:"auth-public-key"`
	APIKey             string            `flag:"api-key"`
	ExternalAuthAddr   string            `flag:"external-auth-addr"`
	ExternalAuth       bool              `flag:"external-auth"`
	UseAccessToken     bool              `flag:"use-access-token"`
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
		return errors.New("must provide both tls-cert and tls-key")
	}

	return runServer(
		ctx,
		int(argsParsed.Port),
		argsParsed.BindAddress,
		argsParsed.InstanceName,
		argsParsed.SignalingAddress,
		argsParsed.TLSCertFile,
		argsParsed.TLSKeyFile,
		argsParsed.TLSAuth,
		argsParsed.AuthPrivateKeyFile,
		argsParsed.AuthPublicKeyFile,
		argsParsed.APIKey,
		argsParsed.ExternalAuthAddr,
		argsParsed.ExternalAuth,
		argsParsed.UseAccessToken,
		logger,
	)
}

func runServer(
	ctx context.Context,
	port int,
	bindAddress string,
	instanceName string,
	signalingAddress string,
	tlsCertFile string,
	tlsKeyFile string,
	tlsAuth bool,
	authPrivateKeyFile string,
	authPublicKeyFile string,
	apiKey string,
	externalAuthAddr string,
	externalAuth bool,
	useAccesssToken bool,
	logger golog.Logger,
) (err error) {
	var serverOpts []rpc.ServerOption
	var authPrivKey *rsa.PrivateKey
	if authPrivateKeyFile != "" {
		//nolint:gosec
		rd, err := os.ReadFile(authPrivateKeyFile)
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
		rd, err := os.ReadFile(authPublicKeyFile)
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

	if bindAddress == "" {
		bindAddress = fmt.Sprintf("localhost:%d", port)
	}

	listener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		return err
	}

	listenerTCPAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return errors.Errorf("expected *net.TCPAddr but got %T", listener.Addr())
	}
	listenerAddr := listenerTCPAddr.String()
	listenerPort := listenerTCPAddr.Port
	secure := tlsCertFile != ""

	var signalingOpts []rpc.DialOption
	if signalingAddress != "" && !secure {
		signalingOpts = append(signalingOpts, rpc.WithInsecure())
	}
	serverOpts = append(serverOpts, rpc.WithExternalListenerAddress(listener.Addr().(*net.TCPAddr)))
	serverOpts = append(serverOpts, rpc.WithWebRTCServerOptions(rpc.WebRTCServerOptions{
		Enable:                    true,
		ExternalSignalingDialOpts: signalingOpts,
		ExternalSignalingAddress:  signalingAddress,
	}))
	if instanceName != "" {
		serverOpts = append(serverOpts, rpc.WithInstanceNames(instanceName))
	}

	if apiKey == "" && authPublicKey == nil {
		serverOpts = append(serverOpts, rpc.WithUnauthenticated())
	} else {
		authEntities := []string{
			listenerAddr,
			bindAddress,
		}
		if instanceName != "" {
			authEntities = append(authEntities, instanceName)
		}
		handler := rpc.MakeSimpleAuthHandler(authEntities, apiKey)
		serverOpts = append(serverOpts, rpc.WithAuthHandler(rpc.CredentialsTypeAPIKey, handler))

		if secure && tlsAuth {
			cert, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
			if err != nil {
				return err
			}
			leaf, err := x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				return err
			}
			serverOpts = append(serverOpts, rpc.WithTLSAuthHandler(leaf.DNSNames, nil))
		}

		if authPublicKey != nil {
			serverOpts = append(serverOpts, rpc.WithAuthHandler("inter-node", rpc.WithPublicKeyProvider(
				rpc.MakeSimpleVerifyEntity(authEntities),
				authPublicKey,
			)))
		}
	}

	if externalAuth {
		if authPrivKey == nil {
			return errors.New("expected auth_private-key")
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
		&echopb.EchoService_ServiceDesc,
		&server.Server{},
		echopb.RegisterEchoServiceHandlerFromEndpoint,
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
	t, err = t.ParseGlob(fmt.Sprintf("%s/*.html", internal.ResolveFile("rpc/examples/echo/server/templates")))
	if err != nil {
		return err
	}
	indexT := t.Lookup("index.html")

	mux := goji.NewMux()
	mux.Handle(pat.Get("/"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type Temp struct {
			WebRTCHost           string
			ExternalAuthAddr     string
			ExternalAuthToEntity string
			Credentials          map[string]interface{}
			AccessToken          string // precomuted access token, bypasses credentials.
		}
		temp := Temp{
			WebRTCHost:           rpcServer.InstanceNames()[0],
			ExternalAuthAddr:     externalAuthAddr,
			ExternalAuthToEntity: rpcServer.InstanceNames()[0],
		}
		if apiKey != "" {
			temp.Credentials = map[string]interface{}{
				"type":    string(rpc.CredentialsTypeAPIKey),
				"payload": apiKey,
			}
		}

		if useAccesssToken {
			precomputedToken, err := computeAccessToken(authPrivKey, listenerAddr, "sub1", rpc.CredentialsTypeAPIKey)
			if err != nil {
				panic(err)
			}
			temp.AccessToken = precomputedToken
		}

		if err := indexT.Execute(w, temp); err != nil {
			panic(err)
		}
	}))
	mux.Handle(pat.Get("/static/*"),
		http.StripPrefix("/static", http.FileServer(http.Dir(internal.ResolveFile("rpc/examples/echo/frontend/dist")))))
	mux.Handle(pat.New("/api/*"), http.StripPrefix("/api", rpcServer.GatewayHandler()))
	mux.Handle(pat.New("/*"), rpcServer.GRPCHandler())

	httpServer, err := utils.NewPossiblySecureHTTPServer(mux, utils.HTTPServerOptions{
		Secure:         secure,
		TLSAuth:        tlsAuth,
		MaxHeaderBytes: rpc.MaxMessageSize,
		Addr:           listenerAddr,
	})
	if err != nil {
		return err
	}

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
		if err := httpServer.Shutdown(ctx); err != nil && utils.FilterOutError(err, context.Canceled) != nil {
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
	if strings.HasPrefix(listenerAddr, "[::]") {
		listenerAddr = fmt.Sprintf("0.0.0.0:%d", listenerPort)
	}
	if listenerTCPAddr.IP.IsLoopback() {
		listenerAddr = fmt.Sprintf("localhost:%d", listenerPort)
	}
	logger.Infow("serving", "url", fmt.Sprintf("%s://%s", scheme, listenerAddr))
	var serveErr error
	if secure {
		serveErr = httpServer.ServeTLS(listener, tlsCertFile, tlsKeyFile)
	} else {
		serveErr = httpServer.Serve(listener)
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}

func computeAccessToken(privKey *rsa.PrivateKey, aud, sub string, credType rpc.CredentialsType) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, rpc.JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  sub,
			Audience: jwt.ClaimStrings{aud},
		},
		AuthCredentialsType: credType,
		AuthMetadata: map[string]string{
			"email": sub,
		},
	})

	var err error
	token.Header["kid"], err = rpc.RSAPublicKeyThumbprint(&privKey.PublicKey)
	if err != nil {
		return "", err
	}

	tokenString, err := token.SignedString(privKey)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}
