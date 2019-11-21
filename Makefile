# generate version number
version=$(shell git describe --tags --long --always --dirty|sed 's/^v//')
binfile=upspicod

all:
	GOARCH=arm64 go build -ldflags "-X main.version=$(version)" $(binfile).go
	-@go fmt

static:
	GOARCH=arm64 go build -ldflags "-X main.version=$(version) -extldflags \"-static\"" -o $(binfile).static $(binfile).go

version:
	@echo $(version)
