all: third_party bin/sage

bin/sage:
	go build --tags "fts5" -o bin/sage .

third_party:
	mkdir -p third_party/lib
	curl -Lo ./third_party/lib/sqlite_vss0.dylib https://github.com/asg017/sqlite-vss/releases/download/v0.1.2/sqlite-vss-v0.1.2-deno-darwin-aarch64.vss0.dylib
	curl -Lo ./third_party/lib/sqlite_vector0.dylib https://github.com/asg017/sqlite-vss/releases/download/v0.1.2/sqlite-vss-v0.1.2-deno-darwin-aarch64.vector0.dylib

clean:
	rm -rf bin third_party
