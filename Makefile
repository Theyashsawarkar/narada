# Build:                             make
# System-wide or packaged install:   make install [PREFIX=/usr/local] [DESTDIR=]
# Per-user, without make:            ./install.sh
PREFIX ?= /usr/local
BINDIR  = $(DESTDIR)$(PREFIX)/bin
SHARE   = $(DESTDIR)$(PREFIX)/share
# systemd user units (narada service install writes its own to ~/.config)
UNITDIR = $(DESTDIR)$(PREFIX)/lib/systemd/user
VERSION = $(shell sed -n 's/^const Version = "\(.*\)"/\1/p' internal/app/app.go)

.PHONY: build install uninstall check test lint dist

build:
	go build -trimpath -o bin/narada ./cmd/narada

install: build
	install -Dm755 bin/narada $(BINDIR)/narada
	sed 's|/usr/bin/narada|$(PREFIX)/bin/narada|' systemd/narada.service > bin/narada.service
	install -Dm644 bin/narada.service $(UNITDIR)/narada.service
	install -Dm644 systemd/narada-mcp.socket $(UNITDIR)/narada-mcp.socket
	install -Dm644 assets/logo.svg $(SHARE)/narada/logo.svg
	install -Dm644 README.md $(SHARE)/doc/narada/README.md
	install -Dm644 CHANGELOG.md $(SHARE)/doc/narada/CHANGELOG.md
	install -Dm644 LICENSE $(SHARE)/licenses/narada/LICENSE

uninstall:
	rm -f $(BINDIR)/narada $(UNITDIR)/narada.service $(UNITDIR)/narada-mcp.socket
	rm -rf $(SHARE)/narada $(SHARE)/doc/narada $(SHARE)/licenses/narada

test:
	go test ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	shellcheck -S warning narada.tmux install.sh scripts/narada-run

# What CI runs.
check: lint test build
	./bin/narada --version
	./bin/narada --help >/dev/null
	python3 scripts/build-site.py --check

# Release binaries (CI does this on a tag).
dist:
	mkdir -p dist
	for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags='-s -w' -o dist/narada-linux-$$arch ./cmd/narada; \
	done
