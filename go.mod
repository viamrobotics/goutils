module go.viam.com/utils

go 1.16

require (
	cloud.google.com/go v0.82.0
	cloud.google.com/go/storage v1.10.0
	github.com/edaniels/golinters v0.0.5-0.20210512224240-495d3b8eed19
	github.com/edaniels/golog v0.0.0-20210326173913-16d408aa7a5e
	github.com/edaniels/gostream v0.0.0-20211028013936-a24d86b4208f
	github.com/fatih/color v1.10.0
	github.com/fsnotify/fsnotify v1.4.9
	github.com/fullstorydev/grpcurl v1.8.0
	github.com/go-errors/errors v1.4.1
	github.com/golangci/golangci-lint v1.39.0
	github.com/grpc-ecosystem/go-grpc-middleware v1.2.2
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.5.0
	github.com/improbable-eng/grpc-web v0.14.0
	github.com/miekg/dns v1.1.35
	github.com/pion/interceptor v0.1.0
	github.com/pion/webrtc/v3 v3.1.7-0.20211028155951-0180ee38051d
	github.com/polyfloyd/go-errorlint v0.0.0-20201127212506-19bd8db6546f
	github.com/pseudomuto/protoc-gen-doc v1.3.2
	go.mongodb.org/mongo-driver v1.5.3
	go.uber.org/goleak v1.1.12
	go.uber.org/multierr v1.7.0
	go.uber.org/zap v1.19.1
	go.viam.com/test v1.1.0
	goji.io v2.0.2+incompatible
	golang.org/x/net v0.0.0-20211020060615-d418f374d309
	golang.org/x/oauth2 v0.0.0-20210615190721-d04028783cf1
	golang.org/x/text v0.3.7 // indirect
	golang.org/x/tools v0.1.7
	google.golang.org/api v0.46.0
	google.golang.org/genproto v0.0.0-20210617175327-b9e0b3197ced
	google.golang.org/grpc v1.38.0
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.1.0
	google.golang.org/protobuf v1.26.0
)

replace github.com/pion/mediadevices => github.com/edaniels/mediadevices v0.0.0-20211022001911-e8e6d6110b1b
