BINARY = clawmeter
PREFIX ?= /usr/local/bin
PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build build-tray install install-tray release-build clean

build:
	go build -o $(BINARY) ./cmd/clawmeter

build-tray:
	go build -tags tray -o $(BINARY) ./cmd/clawmeter

install: build
	install -m 755 $(BINARY) $(PREFIX)/$(BINARY)

install-tray: build-tray
	install -m 755 $(BINARY) $(PREFIX)/$(BINARY)

release-build:
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		echo "Building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags='-s -w' -o $(BINARY)-$$os-$$arch ./cmd/clawmeter; \
	done

clean:
	rm -f $(BINARY) $(BINARY)-*
