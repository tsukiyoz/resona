//go:build !windows

package main

import (
	"context"
	"errors"
	"io"
)

func listProcesses(io.Writer, string) error {
	return errors.New("list requires Windows; analyze supports existing macOS CSV on any platform")
}
func collect(context.Context, collectOptions, io.Writer) error {
	return errors.New("collect requires Windows; use tools/desktop-metrics.c on macOS")
}
