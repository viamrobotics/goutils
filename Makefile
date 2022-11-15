TOOL_BIN = bin/gotools/$(shell uname -s)-$(shell uname -m)

PATH_WITH_TOOLS="`pwd`/$(TOOL_BIN):`pwd`/node_modules/.bin:${PATH}"

setup-cert:
	cd etc && bash ./setup_cert.sh

setup-priv-key:
	cd etc && bash ./setup_priv_key.sh

build: build-web build-go

build-go: buf-go
	go build ./...

build-web: buf-web
	export NODE_OPTIONS=--openssl-legacy-provider && node --version 2>/dev/null || unset NODE_OPTIONS;\
	cd rpc/js && npm install && npx webpack && \
	cd ../examples/echo/frontend && npm install && npx webpack && \
	cd ../../fileupload/frontend && npm install && npx webpack

tool-install:
	GOBIN=`pwd`/$(TOOL_BIN) go install google.golang.org/protobuf/cmd/protoc-gen-go \
		github.com/bufbuild/buf/cmd/buf \
		github.com/bufbuild/buf/cmd/protoc-gen-buf-breaking \
		github.com/bufbuild/buf/cmd/protoc-gen-buf-lint \
		github.com/pseudomuto/protoc-gen-doc/cmd/protoc-gen-doc \
		google.golang.org/grpc/cmd/protoc-gen-go-grpc \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2 \
		github.com/edaniels/golinters/cmd/combined \
		github.com/golangci/golangci-lint/cmd/golangci-lint \
		github.com/AlekSi/gocov-xml \
		github.com/axw/gocov/gocov

buf: buf-go buf-web

buf-go: tool-install
	PATH=$(PATH_WITH_TOOLS) buf lint
	PATH=$(PATH_WITH_TOOLS) buf generate

buf-web: tool-install
	npm install
	PATH=$(PATH_WITH_TOOLS) buf lint
	PATH=$(PATH_WITH_TOOLS) buf generate --template ./etc/buf.web.gen.yaml
	PATH=$(PATH_WITH_TOOLS) buf generate --template ./etc/buf.web.gen.yaml buf.build/googleapis/googleapis

lint: tool-install
	PATH=$(PATH_WITH_TOOLS) buf lint
	export pkgs="`go list -f '{{.Dir}}' ./... | grep -v /proto/`" && echo "$$pkgs" | xargs go vet -vettool=$(TOOL_BIN)/combined
	GOGC=50 $(TOOL_BIN)/golangci-lint run -v --fix --config=./etc/.golangci.yaml

cover:
	go test -tags=no_skip -race -coverprofile=coverage.txt ./...
	PATH=$(PATH_WITH_TOOLS) gocov convert coverage.txt | PATH=$(PATH_WITH_TOOLS) gocov-xml > coverage.xml

test:
	go test -tags=no_skip -race ./...

# examples

example-echo/%: build-web
	$(MAKE) -C rpc/examples/echo $*

example-fileupload/%: build-web
	$(MAKE) -C rpc/examples/fileupload $*
