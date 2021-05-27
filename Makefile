
goformat:
	go install golang.org/x/tools/cmd/goimports
	gofmt -s -w .
	goimports -w -local=go.viam.com/core `go list -f '{{.Dir}}' ./... | grep -Ev "proto"`

format: goformat

build: build-go

build-go:
	go build ./...

lint: goformat
	go install github.com/edaniels/golinters/cmd/combined
	go list -f '{{.Dir}}' ./... | grep -v gen | xargs go vet -vettool=`go env GOPATH`/bin/combined
	go list -f '{{.Dir}}' ./... | grep -v gen | xargs go run github.com/golangci/golangci-lint/cmd/golangci-lint run -v

cover:
	go test -coverprofile=coverage.txt ./...

test:
	go test ./...

