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

.PHONY: build install clean deps tidy vet fmt help skills-init skills-init-claude skills-init-cursor skills-init-agent

## build: compile binary to project root
build:
	go build -ldflags "$(LDFLAGS)" -o ./$(BINARY_DIR)/$(BINARY) $(CMD_PATH)

## install: install binary to $GOPATH/bin and set up default config
install:
	go install -ldflags "$(LDFLAGS)" $(CMD_PATH)
	@mkdir -p ~/.config/ffc/
	@if [ ! -f ~/.config/ffc/config.yaml ]; then \
		cp config.example.yaml ~/.config/ffc/config.yaml; \
		echo "Created ~/.config/ffc/config.yaml from example — edit it with your site details."; \
	else \
		echo "~/.config/ffc/config.yaml already exists, skipping copy."; \
	fi

## deps: add new dependencies (charmbracelet)
deps:
	go get charm.land/lipgloss/v2
	go get github.com/charmbracelet/huh
	go get github.com/charmbracelet/huh/spinner

## tidy: install/update all dependencies
tidy: deps
	go mod tidy

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go files
fmt:
	gofmt -w .

## clean: remove compiled binary
clean:
	rm -f ./$(BINARY_DIR)/$(BINARY)

## help: print this help
help:
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## skills-init: Initialize skills for AI agents
skills-init:
	$(MAKE) skills-init-claude skills-init-cursor skills-init-agent

## skills-init-claude: Initialize skills for Claude
skills-init-claude:
	mkdir -p .claude/skills/ && rm -rf .claude/skills/* && cd .claude/skills/ && ln -s ../../.agents/skills/*/ .
	echo "Skills initialized for Claude"

## skills-init-cursor: Initialize skills for Cursor
skills-init-cursor:
	mkdir -p .cursor/skills/ && rm -rf .cursor/skills/* && cd .cursor/skills/ && ln -s ../../.agents/skills/*/ .
	echo "Skills initialized for Cursor"

## skills-init-agent: Initialize skills for Agent
skills-init-agent:
	mkdir -p .agent/skills/ && rm -rf .agent/skills/* && cd .agent/skills/ && ln -s ../../.agents/skills/*/ .
	echo "Skills initialized for Antigravity, Gemini CLI, Codex, ...etc"