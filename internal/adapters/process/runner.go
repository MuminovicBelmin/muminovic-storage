// Package process is the only adapter that starts operating-system commands.
package process

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"
)

type Command struct {
	Program     string
	Args        []string
	Description string
	Timeout     time.Duration
	Stderr      io.Writer
}
type Runner interface {
	Run(context.Context, Command) ([]byte, error)
	Stream(context.Context, Command, io.Writer) error
}
type Native struct{}
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("command output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (Native) Run(ctx context.Context, c Command) ([]byte, error) {
	out := &limitedBuffer{limit: 32 << 20}
	err := Native{}.Stream(ctx, c, out)
	return out.Bytes(), err
}
func (Native) Stream(ctx context.Context, c Command, out io.Writer) error {
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	command := exec.CommandContext(ctx, c.Program, c.Args...)
	command.Stdout = out
	command.Stderr = io.Discard
	if c.Stderr != nil {
		command.Stderr = c.Stderr
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed (command output withheld to protect credentials)", c.Description)
	}
	return nil
}
