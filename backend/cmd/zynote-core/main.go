package main

import (
	"context"
	"os"

	"github.com/EveSunMaple/Zynote/backend/internal/app"
	"github.com/EveSunMaple/Zynote/backend/internal/helper"
	"github.com/EveSunMaple/Zynote/backend/internal/transport"
)

func main() {
	core := app.New()
	server := transport.NewServer(core)
	core.Wire(server)
	native := helper.NewNative("pdf")
	core.SetNative(native)
	defer native.Dispose()
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
