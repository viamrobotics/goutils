setup-cert:
	mise r setup-cert

setup-priv-key:
	mise r setup-priv-key

build: build-go

build-go:
	mise r build-go

tool-install:
	mise install

buf: buf-go

buf-go: tool-install
	mise r buf-go

lint: tool-install lint-go

lint-go: tool-install
	mise r lint-go

cover: tool-install
	mise r cover

test: test-go

test-go: tool-install
	mise r test-go

# examples

example-echo/%:
	mise r example-echo $*
