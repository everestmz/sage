package main

import (
	"fmt"
	"io"
	"os"
)

type ReadLogger struct {
	io.ReadCloser
}

func (rl *ReadLogger) Read(p []byte) (n int, err error) {
	n, err = rl.ReadCloser.Read(p)
	fmt.Fprintln(os.Stderr, "READ", string(p))
	return n, err
}

func (rl *ReadLogger) Close() error {
	return rl.Close()
}

type WriteLogger struct {
	io.Writer
}

func (wl *WriteLogger) Write(p []byte) (n int, err error) {
	fmt.Fprintln(os.Stderr, "WRITING", string(p))
	return wl.Writer.Write(p)
}
