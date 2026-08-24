.PHONY: build test test-race vet check clean

build:
	go build -trimpath -o tailg ./cmd/tailg

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

check: test vet build

clean:
	go clean

