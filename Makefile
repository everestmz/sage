all: bin/sage

# Set install location
PREFIX ?= /usr/local
INSTALL_PATH = $(PREFIX)/bin

bin/sage:
	go build -o bin/sage .

generate:
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative rpc/languageserver/LanguageServerState.proto

install: bin/sage
	@echo "Installing to $(INSTALL_PATH)/sage..."
	@mkdir -p $(INSTALL_PATH)
	@install -m 755 bin/sage $(INSTALL_PATH)/sage

# TODO: fix this up, we're not using vss right now so should be able to get rid of
third_party:
	mkdir -p third_party/lib
	curl -Lo ./third_party/lib/sqlite_vss0.dylib https://github.com/asg017/sqlite-vss/releases/download/v0.1.2/sqlite-vss-v0.1.2-deno-darwin-aarch64.vss0.dylib
	curl -Lo ./third_party/lib/sqlite_vector0.dylib https://github.com/asg017/sqlite-vss/releases/download/v0.1.2/sqlite-vss-v0.1.2-deno-darwin-aarch64.vector0.dylib

clean:
	rm -rf bin third_party
