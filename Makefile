BINDIR := $(HOME)/.bin

.PHONY: all build install clean

all: build

build:
	go build -o server ./cmd/server
	go build -o client ./cmd/client

install: build
	install -d $(BINDIR)
	install -m 755 server $(BINDIR)/claudesrv-server
	install -m 755 client $(BINDIR)/claudesrv-client

clean:
	rm -f server client
