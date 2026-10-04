# ffc — Foxmayn Frappe CLI

BINARY  := ffc
BINARY_DIR := bin
CMD_PATH := ./cmd/ffc

# Build-time version injection
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/nasroykh/foxmayn_frappe_cli/internal/version.Version=$(VERSION) \
	-X github.com/nasroykh/foxmayn_frappe_cli/internal/version.Commit=$(COMMIT) \
	-X github.com/nasroykh/foxmayn_frappe_cli/internal/version.Date=$(DATE)

# Analysis tools, run with go run at pinned versions (same as CI).
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0

.PHONY: build install clean tidy vet fmt test lint vuln contract help skills-init skills-init-claude skills-init-cursor skills-init-agent

## build: compile binary to ./bin/ffc
build:
	go build -ldflags "$(LDFLAGS)" -o ./$(BINARY_DIR)/$(BINARY) $(CMD_PATH)

## install: install binary to $GOPATH/bin and set up default config
install:
	go install -ldflags "$(LDFLAGS)" $(CMD_PATH)
	@mkdir -m 700 -p ~/.config/ffc/
	@if [ ! -f ~/.config/ffc/config.yaml ]; then \
		install -m 600 config.example.yaml ~/.config/ffc/config.yaml; \
		echo "Created ~/.config/ffc/config.yaml from example — edit it with your site details."; \
	else \
		echo "~/.config/ffc/config.yaml already exists, skipping copy."; \
	fi

## tidy: install/update all dependencies
tidy:
	go mod tidy

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go files
fmt:
	gofmt -w .

## test: run all tests with the race detector
test:
	go test -race ./...

## lint: gofmt check, go vet and staticcheck
lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	go run $(STATICCHECK) ./...

## contract: run the contract tests against a real site (SITE=<ffc config site>; it writes test data)
contract:
	@if [ -z "$(SITE)" ]; then echo "usage: make contract SITE=<site in your ffc config>"; exit 1; fi
	FFC_CONTRACT_SITE=$(SITE) go test -tags contract -count=1 -run TestContract -v ./internal/cmd/

## vuln: report reachable known vulnerabilities (govulncheck)
vuln:
	go run $(GOVULNCHECK) ./...

## clean: remove compiled binary
clean:
	rm -f ./$(BINARY_DIR)/$(BINARY)

## help: print this help
help:
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## skills-init: Link the ffc skills (skills/) into each AI agent's skills folder
skills-init:
	$(MAKE) skills-init-claude skills-init-cursor skills-init-agent

## skills-init-claude: Initialize skills for Claude
skills-init-claude:
	mkdir -p .claude/skills/ && find .claude/skills/ -mindepth 1 -maxdepth 1 -type l -delete && cd .claude/skills/ && ln -s ../../skills/*/ .
	echo "Skills initialized for Claude"

## skills-init-cursor: Initialize skills for Cursor
skills-init-cursor:
	mkdir -p .cursor/skills/ && find .cursor/skills/ -mindepth 1 -maxdepth 1 -type l -delete && cd .cursor/skills/ && ln -s ../../skills/*/ .
	echo "Skills initialized for Cursor"

## skills-init-agent: Initialize skills for Agent
skills-init-agent:
	mkdir -p .agent/skills/ && find .agent/skills/ -mindepth 1 -maxdepth 1 -type l -delete && cd .agent/skills/ && ln -s ../../skills/*/ .
	echo "Skills initialized for Antigravity, Gemini CLI, Codex, ...etc"