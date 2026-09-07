package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title: "Resona", Width: 1280, Height: 820,
		MinWidth: 960, MinHeight: 640,
		BackgroundColour: &options.RGBA{R: 20, G: 24, B: 26, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		Bind:             []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
