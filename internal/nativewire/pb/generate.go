//go:build ignore

// Run from this directory via go generate. No global protoc-gen-go is needed.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	version, err := exec.Command("protoc", "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "libprotoc 29.3" {
		fmt.Fprintln(os.Stderr, "generation requires protoc 29.3 on PATH")
		os.Exit(1)
	}
	tmp, err := os.MkdirTemp("", "resona-protoc-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	plugin := filepath.Join(tmp, "protoc-gen-go")
	if runtime.GOOS == "windows" {
		plugin += ".exe"
	}
	// The root go.mod pins the generator and runtime to the same module version.
	for _, args := range [][]string{
		{"go", "build", "-o", plugin, "google.golang.org/protobuf/cmd/protoc-gen-go"},
		{"protoc", "--plugin=protoc-gen-go=" + plugin, "--go_out=.", "--go_opt=paths=source_relative", "control.proto"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			panic(err)
		}
	}
}
