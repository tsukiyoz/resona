package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/config"
	"github.com/tsukiyoz/resona/internal/credentials"
	"github.com/tsukiyoz/resona/internal/desktopipc"
	"github.com/tsukiyoz/resona/internal/iconcache"
	"github.com/tsukiyoz/resona/internal/protocol"
	"github.com/tsukiyoz/resona/internal/protocol/native"
	"github.com/tsukiyoz/resona/internal/protocol/ts3"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	store, err := config.NewDefault()
	if err != nil {
		return fmt.Errorf("cannot open Resona profiles: %w", err)
	}
	connector, err := ts3.NewDefault(iconcache.NewDefault())
	if err != nil {
		return fmt.Errorf("cannot initialize Resona identity: %w", err)
	}
	service, err := client.NewWithPasswordStore(store, protocol.Router{TS3: connector, Native: native.Connector{}}, credentials.New())
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return desktopipc.Run(ctx, service, os.Stdin, os.Stdout)
}
