BINARY = clawmeter
PREFIX ?= /usr/local/bin

.PHONY: build build-tray install install-tray clean

build:
	go build -o $(BINARY) ./cmd/clawmeter

build-tray:
	go build -tags tray -o $(BINARY) ./cmd/clawmeter

install: build
	install -m 755 $(BINARY) $(PREFIX)/$(BINARY)

install-tray: build-tray
	install -m 755 $(BINARY) $(PREFIX)/$(BINARY)

clean:
	rm -f $(BINARY)
