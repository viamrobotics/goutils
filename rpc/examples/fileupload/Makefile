all: build
.PHONY: all

build:
	cd frontend && npx webpack

run: build
	go run server/cmd/main.go

clean:
	rm -rf frontend/dist
