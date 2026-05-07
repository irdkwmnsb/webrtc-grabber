package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/recorder"
	"github.com/lmittmann/tint"
)

func main() {
	slog.SetDefault(slog.New(
		tint.NewHandler(os.Stderr, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.Kitchen,
		}),
	))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	config, err := recorder.LoadRecorderConfig()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if err = os.MkdirAll(config.RecordingsDirectory, os.ModePerm); err != nil {
		slog.Warn("failed to create output directory", "error", err)
	}

	app := fiber.New()
	server := recorder.NewRecorderServer(config, app)

	server.SetupRouting(ctx)
	app.Static("/recordings", config.RecordingsDirectory, fiber.Static{
		Browse:        true,
		CacheDuration: time.Second,
	})
	app.Static("/record", "./asset/record.html")

	go func() {
		<-ctx.Done()
		slog.Info("shutdown signal received")
		if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
		}
	}()

	if err := app.Listen(":8001"); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
