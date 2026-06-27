# Reveal Notes Server — Makefile
# ------------------------------------------------------------------
# Go cross-compilation is lightweight: GOOS + GOARCH is all you need.
# No heavy toolchain setup — a full cross-build finishes in a few seconds.

# ------------------------------------------------------------------
# Configuration
# ------------------------------------------------------------------
BINARY        := notes-server
CMD_DIR       := ./cmd/notes-server
UPLOAD_BINARY := upload-presentation
UPLOAD_CMD_DIR := ./cmd/upload-presentation
GO            := go
GOFLAGS       := -ldflags="-s -w"
DIST_DIR      := dist

# GOOS / GOARCH — defaults to the host platform
GOOS          ?=
GOARCH        ?=
GOARM         ?=
GO_ENV        := $(if $(GOOS),GOOS=$(GOOS) )$(if $(GOARCH),GOARCH=$(GOARCH) )$(if $(GOARM),GOARM=$(GOARM) )

# ------------------------------------------------------------------
# Default target: build for the current platform
# ------------------------------------------------------------------
.PHONY: build
build:
	$(GO_ENV)$(GO) build $(GOFLAGS) -o $(BINARY)$(if $(filter windows,$(GOOS)),.exe) $(CMD_DIR)
	$(GO_ENV)$(GO) build $(GOFLAGS) -o $(UPLOAD_BINARY)$(if $(filter windows,$(GOOS)),.exe) $(UPLOAD_CMD_DIR)
# ------------------------------------------------------------------
# Convenience targets — single-platform builds
# ------------------------------------------------------------------
.PHONY: build-linux-amd64
build-linux-amd64:
	GOOS=linux   GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-amd64   $(CMD_DIR)

.PHONY: build-linux-arm64
build-linux-arm64:
	GOOS=linux   GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-arm64   $(CMD_DIR)

.PHONY: build-windows-amd64
build-windows-amd64:
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe $(CMD_DIR)

.PHONY: build-darwin-amd64
build-darwin-amd64:
	GOOS=darwin  GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-amd64  $(CMD_DIR)

.PHONY: build-darwin-arm64
build-darwin-arm64:
	GOOS=darwin  GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-arm64  $(CMD_DIR)

# ------------------------------------------------------------------
# Cross: build all supported platforms in one shot
# ------------------------------------------------------------------
.PHONY: cross
cross: clean-dist build-linux-amd64 build-linux-arm64 build-windows-amd64 build-darwin-amd64 build-darwin-arm64
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
	@echo "  make build-linux-amd64    Build for linux/amd64"
	@echo "  make build-linux-arm64    Build for linux/arm64"
	@echo "  make build-windows-amd64  Build for windows/amd64"
	@echo "  make build-darwin-amd64   Build for darwin/amd64  (Intel Mac)"
	@echo "  make build-darwin-arm64   Build for darwin/arm64  (Apple Silicon)"
	@echo "  make cross                Build all platforms into dist/"
	@echo "  make build-upload         Build only the upload CLI"
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
	@echo "  make build GOOS=linux GOARCH=arm GOARM=7"
