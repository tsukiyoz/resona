package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/config"
	"github.com/tsukiyoz/resona/internal/credentials"
	"github.com/tsukiyoz/resona/internal/desktopipc"
	"github.com/tsukiyoz/resona/internal/diagnostics"
	"github.com/tsukiyoz/resona/internal/protocol"
	"github.com/tsukiyoz/resona/internal/protocol/native"
	"github.com/tsukiyoz/resona/internal/version"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)).With("component", "resona-core"))
	if !version.Requested(os.Args[1:]) {
		slog.SetDefault(slog.New(diagnostics.New()).With("component", "resona-core"))
		slog.Info("client started", "version", version.Current().String())
	}
	exitCode := 0
	if err := run(); err != nil {
		slog.Error("core stopped", "error", err)
		exitCode = 1
	}
	if !version.Requested(os.Args[1:]) {
		slog.Info("client stopped")
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "print build version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("resona-core", version.Current())
		return nil
	}
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	store, err := config.NewDefault()
	if err != nil {
		return fmt.Errorf("cannot open Resona profiles: %w", err)
	}
	service, err := client.NewWithPasswordStore(store, defaultConnector(), credentials.New())
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return desktopipc.Run(ctx, service, os.Stdin, os.Stdout)
}

func defaultConnector() client.RemoteConnector {
	return protocol.NewLazyConnector(func() (client.RemoteConnector, error) {
		return native.NewDefault()
	})
}
