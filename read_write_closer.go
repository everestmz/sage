package main

import (
	"io"
	"os"
)

type CombinedReadWriteCloser struct {
	io.Reader
	io.Writer
	Closer func() error

	readFile  *os.File
	writeFile *os.File
}

func (c *CombinedReadWriteCloser) Read(p []byte) (int, error) {
	read, err := c.Reader.Read(p)

	if c.readFile != nil {
		c.readFile.Write(p)
	}

	return read, err
}

func (c *CombinedReadWriteCloser) Write(p []byte) (int, error) {
	if c.writeFile != nil {
		c.writeFile.Write(p)
	}
	return c.Writer.Write(p)
}

func (c *CombinedReadWriteCloser) Close() error {
	if c.readFile != nil {
		c.readFile.Close()
	}

	if c.writeFile != nil {
		c.writeFile.Close()
	}

	return c.Closer()
}
