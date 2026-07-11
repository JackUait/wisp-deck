.PHONY: build install test clean lint release sync-version help

# Build the Go binary. Stamp it with the VERSION file (like release builds)
# so --version and the About card report the real version instead of "dev".
build:
	@echo "Building wisp-deck-tui..."
	go build -ldflags "-X main.Version=$$(cat VERSION)" -o bin/wisp-deck-tui ./cmd/wisp-deck-tui
	@codesign --sign - --force bin/wisp-deck-tui
	@echo "✓ Built bin/wisp-deck-tui"

# Install to local bin
install: build
	@echo "Installing to ~/.local/bin..."
	mkdir -p $(HOME)/.local/bin
	cp bin/wisp-deck-tui $(HOME)/.local/bin/
	@codesign --sign - --force $(HOME)/.local/bin/wisp-deck-tui
	@# Re-signing resets the Gatekeeper first-run assessment — pay it now, not
	@# on the next modal open in a live session.
	@$(HOME)/.local/bin/wisp-deck-tui --version >/dev/null 2>&1 || true
	@echo "✓ Installed wisp-deck-tui"

# Run tests
test:
	@echo "Running Go tests..."
	go test ./...
	@echo "Running bash tests..."
	./run-tests.sh

# Run Go tests only
test-go:
	go test -v ./...

# Run bash tests only
test-bash:
	./run-tests.sh

# Clean build artifacts
clean:
	rm -f bin/wisp-deck-tui
	go clean

# Lint Go code
lint:
	@echo "Running golangci-lint..."
	golangci-lint run ./...
	@echo "Running shellcheck..."
	find lib bin ghostty -name '*.sh' -exec shellcheck {} +

# Create a new release (tag, GitHub release with binaries)
release:
	@bash scripts/release.sh

# Sync package.json version with VERSION file
sync-version:
	@node -e "const p=require('./package.json');const v=require('fs').readFileSync('VERSION','utf8').trim();p.version=v;require('fs').writeFileSync('package.json',JSON.stringify(p,null,2)+'\n');console.log('Synced package.json to '+v)"

# Show help
help:
	@echo "Wisp Deck Build Targets:"
	@echo "  make build   - Build the Go binary"
	@echo "  make install - Install to ~/.local/bin"
	@echo "  make test    - Run all tests (Go + bash)"
	@echo "  make clean   - Remove build artifacts"
	@echo "  make lint    - Run linters"
	@echo "  make release - Create a new release"
