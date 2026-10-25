.PHONY: build test test-race install clean

build:
	CGO_ENABLED=0 go build -buildvcs=false -o bin/rmm-server .

test:
	go test ./...

# ARM64 systems with 39-bit VMA cannot run Go's race detector.
test-race:
	go test -race ./...

install: build
	install -Dm755 bin/rmm-server $(HOME)/bin/rmm-server

clean:
	rm -rf bin
