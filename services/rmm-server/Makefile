.PHONY: build collector test test-race install clean

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false -o ../../bin/linux-arm64/rmm-server .

collector:
	CGO_ENABLED=0 go build -buildvcs=false -o ../../bin/native/rmm-collector .

test:
	go test ./...

# ARM64 systems with 39-bit VMA cannot run Go's race detector.
test-race:
	go test -race ./...

install: build
	@test "$$(uname -sm)" = "Linux aarch64" || { echo 'Server installation requires Linux aarch64.' >&2; exit 1; }
	install -Dm755 ../../bin/linux-arm64/rmm-server $(HOME)/bin/rmm-server

clean:
	rm -rf ../../bin/native ../../bin/linux-arm64
