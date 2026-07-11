.PHONY: build test vet fmt check clean

build:
	mkdir -p bin
	go build -o bin/arbor ./cmd/arbor

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test ./...
	go build ./cmd/arbor

clean:
	go clean
	rm -rf bin dist
