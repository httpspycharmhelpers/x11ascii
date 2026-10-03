package main

import (
	"io"
	"os"
	"os/exec"
)

type cmdSource struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	w, h   int
	nbytes int
}

func newCmdSource(command string, w, h int) (*cmdSource, error) {
	c := exec.Command("sh", "-c", command)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return nil, err
	}
	return &cmdSource{cmd: c, stdout: stdout, w: w, h: h, nbytes: w * h * 3}, nil
}

func (c *cmdSource) Size() (int, int) { return c.w, c.h }

func (c *cmdSource) Grab(f *Frame) error {
	f.alloc(c.w, c.h)
	_, err := io.ReadFull(c.stdout, f.Pix[:c.nbytes])
	return err
}

func (c *cmdSource) Close() error {
	c.stdout.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}
