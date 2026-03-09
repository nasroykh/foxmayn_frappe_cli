# ffc — Foxmayn Frappe CLI

BINARY := ffc
BUILD_DIR := .
CMD_PATH := ./cmd/ffc

.PHONY: build install clean tidy

## build: compile and place binary in the project root
build:
	go build -o $(BUILD_DIR)/$(BINARY) $(CMD_PATH)

## install: install binary to $GOPATH/bin (or ~/go/bin by default)
install:
	go install $(CMD_PATH)

## tidy: install/update dependencies
tidy:
	go mod tidy

## clean: remove compiled binary
clean:
	rm -f $(BUILD_DIR)/$(BINARY)

## help: print this help
help:
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
