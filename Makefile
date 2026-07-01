# Reveal Notes Server — Makefile
# ------------------------------------------------------------------
# Local builds keep the historical top-level binaries for convenience,
# while release targets write predictable platform-specific paths under
# dist/ and package both executables together.
# ------------------------------------------------------------------

SHELL := /bin/sh

BINARY         := notes-server
UPLOAD_BINARY   := upload-presentation
CMD_DIR        := ./cmd/notes-server
UPLOAD_CMD_DIR := ./cmd/upload-presentation
GO             := go
GOFLAGS        := -trimpath -ldflags="-s -w"
DIST_DIR       := dist
PACKAGE_NAME   := remote-notes-server

GOOS   ?= $(shell $(GO) env GOOS)
GOARCH ?= $(shell $(GO) env GOARCH)
GOARM  ?=
VERSION ?= dev
CGO_ENABLED ?= 0

GO_ENV := CGO_ENABLED=$(CGO_ENABLED) $(if $(GOOS),GOOS=$(GOOS) )$(if $(GOARCH),GOARCH=$(GOARCH) )$(if $(GOARM),GOARM=$(GOARM) )
PLATFORM_EXT := $(if $(filter windows,$(GOOS)),.exe,)
DIST_BINARY := $(DIST_DIR)/$(BINARY)-$(GOOS)-$(GOARCH)$(PLATFORM_EXT)
DIST_UPLOAD_BINARY := $(DIST_DIR)/$(UPLOAD_BINARY)-$(GOOS)-$(GOARCH)$(PLATFORM_EXT)
RELEASE_STAGE_DIR := $(DIST_DIR)/release/$(PACKAGE_NAME)-$(VERSION)-$(GOOS)-$(GOARCH)
RELEASE_ARCHIVE := $(DIST_DIR)/$(PACKAGE_NAME)-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz
RELEASE_CHECKSUM := $(RELEASE_ARCHIVE).sha256

.DEFAULT_GOAL := all

.PHONY: all
all: build build-upload

.PHONY: build
build:
	$(GO_ENV)$(GO) build $(GOFLAGS) -o $(BINARY)$(if $(filter windows,$(GOOS)),.exe) $(CMD_DIR)

.PHONY: build-upload
build-upload:
	$(GO_ENV)$(GO) build $(GOFLAGS) -o $(UPLOAD_BINARY)$(if $(filter windows,$(GOOS)),.exe) $(UPLOAD_CMD_DIR)

.PHONY: dist-build
dist-build: $(DIST_BINARY) $(DIST_UPLOAD_BINARY)

$(DIST_BINARY):
	@mkdir -p $(DIST_DIR)
	$(GO_ENV)$(GO) build $(GOFLAGS) -o $@ $(CMD_DIR)

$(DIST_UPLOAD_BINARY):
	@mkdir -p $(DIST_DIR)
	$(GO_ENV)$(GO) build $(GOFLAGS) -o $@ $(UPLOAD_CMD_DIR)

.PHONY: release-package
release-package: $(RELEASE_ARCHIVE) $(RELEASE_CHECKSUM)

$(RELEASE_ARCHIVE): $(DIST_BINARY) $(DIST_UPLOAD_BINARY)
	@rm -rf $(RELEASE_STAGE_DIR)
	@mkdir -p $(RELEASE_STAGE_DIR)
	@cp $(DIST_BINARY) $(RELEASE_STAGE_DIR)/$(BINARY)$(PLATFORM_EXT)
	@cp $(DIST_UPLOAD_BINARY) $(RELEASE_STAGE_DIR)/$(UPLOAD_BINARY)$(PLATFORM_EXT)
	@tar -czf $@ -C $(DIST_DIR)/release $(PACKAGE_NAME)-$(VERSION)-$(GOOS)-$(GOARCH)

$(RELEASE_CHECKSUM): $(RELEASE_ARCHIVE)
	@sha256sum $(RELEASE_ARCHIVE) > $@

# ------------------------------------------------------------------
# Convenience targets — single-platform builds
# ------------------------------------------------------------------

.PHONY: build-linux-amd64
build-linux-amd64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-amd64   $(CMD_DIR)

.PHONY: build-upload-linux-amd64
build-upload-linux-amd64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(UPLOAD_BINARY)-linux-amd64 $(UPLOAD_CMD_DIR)

.PHONY: build-linux-arm64
build-linux-arm64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-arm64   $(CMD_DIR)

.PHONY: build-upload-linux-arm64
build-upload-linux-arm64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(UPLOAD_BINARY)-linux-arm64 $(UPLOAD_CMD_DIR)

.PHONY: build-windows-amd64
build-windows-amd64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe $(CMD_DIR)

.PHONY: build-upload-windows-amd64
build-upload-windows-amd64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(UPLOAD_BINARY)-windows-amd64.exe $(UPLOAD_CMD_DIR)

.PHONY: build-darwin-amd64
build-darwin-amd64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-amd64  $(CMD_DIR)

.PHONY: build-upload-darwin-amd64
build-upload-darwin-amd64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(UPLOAD_BINARY)-darwin-amd64 $(UPLOAD_CMD_DIR)

.PHONY: build-darwin-arm64
build-darwin-arm64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-arm64  $(CMD_DIR)

.PHONY: build-upload-darwin-arm64
build-upload-darwin-arm64:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(UPLOAD_BINARY)-darwin-arm64 $(UPLOAD_CMD_DIR)

# ------------------------------------------------------------------
# Cross: build all supported platforms in one shot
# ------------------------------------------------------------------

.PHONY: cross
cross: clean-dist build-linux-amd64 build-upload-linux-amd64 build-linux-arm64 build-upload-linux-arm64 build-windows-amd64 build-upload-windows-amd64 build-darwin-amd64 build-upload-darwin-amd64 build-darwin-arm64 build-upload-darwin-arm64
	@echo "Cross-compile done. Binaries in $(DIST_DIR)/:"
	@ls -lh $(DIST_DIR)/

# ------------------------------------------------------------------
# Utilities
# ------------------------------------------------------------------

.PHONY: run
run: build
	./$(BINARY)

.PHONY: test
test:
	$(GO) test ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: clean
clean:
	rm -f $(BINARY) $(BINARY).exe $(UPLOAD_BINARY) $(UPLOAD_BINARY).exe

.PHONY: clean-dist
clean-dist:
	rm -rf $(DIST_DIR)

.PHONY: distclean
distclean: clean clean-dist

# ------------------------------------------------------------------
# Help
# ------------------------------------------------------------------

.PHONY: help
help:
	@echo "Reveal Notes Server — Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make                      Build notes-server and upload-presentation"
	@echo "  make dist-build            Build both binaries into dist/"
	@echo "  make release-package       Build + package both binaries into a tar.gz"
	@echo "  make build-linux-amd64          Build notes-server for linux/amd64"
	@echo "  make build-upload-linux-amd64   Build upload-presentation for linux/amd64"
	@echo "  make build-linux-arm64          Build notes-server for linux/arm64"
	@echo "  make build-upload-linux-arm64   Build upload-presentation for linux/arm64"
	@echo "  make build-windows-amd64        Build notes-server for windows/amd64"
	@echo "  make build-upload-windows-amd64 Build upload-presentation for windows/amd64"
	@echo "  make build-darwin-amd64         Build notes-server for darwin/amd64  (Intel Mac)"
	@echo "  make build-upload-darwin-amd64  Build upload-presentation for darwin/amd64"
	@echo "  make build-darwin-arm64         Build notes-server for darwin/arm64  (Apple Silicon)"
	@echo "  make build-upload-darwin-arm64  Build upload-presentation for darwin/arm64"
	@echo "  make cross                      Build all platforms into dist/"
	@echo "  make build-upload               Build only the upload CLI"
	@echo "  make run                  Build and run"
	@echo "  make test                 Run tests"
	@echo "  make vet                  Run go vet"
	@echo "  make fmt                  Run go fmt"
	@echo "  make tidy                 Run go mod tidy"
	@echo "  make clean                Remove local binaries"
	@echo "  make clean-dist           Remove dist/"
	@echo "  make distclean            Remove all build artifacts"
	@echo ""
	@echo "Override GOOS/GOARCH for a custom build:"
	@echo "  make release-package VERSION=v1.2.3 GOOS=linux GOARCH=arm64"
