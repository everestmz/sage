package main

import "io"

type CombinedReadWriteCloser struct {
	io.Reader
	io.Writer
	Closer func() error
}

func (c *CombinedReadWriteCloser) Read(p []byte) (int, error) {
	return c.Reader.Read(p)
}

func (c *CombinedReadWriteCloser) Write(p []byte) (int, error) {
	return c.Writer.Write(p)
}

func (c *CombinedReadWriteCloser) Close() error {
	return c.Closer()
}
