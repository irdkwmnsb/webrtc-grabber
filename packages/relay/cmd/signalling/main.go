package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/lmittmann/tint"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/asset"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/signalling"
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

	cfgManager, err := config.NewManager("conf")
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	cfg := cfgManager.Get()

	app := fiber.New(fiber.Config{
		BodyLimit: 50 * 1024 * 1024,
	})

	server, err := signalling.NewServer(ctx, &cfg, app)
	if err != nil {
		slog.Error("can not start signalling server", "error", err)
		os.Exit(1)
	}

	defer server.Close()

	cfgManager.SetUpdateCallback(func(newCfg *config.AppConfig) {
		server.UpdateConfig(newCfg)
	})

	server.SetupWebSocketsAndApi()

	if cfg.Debug.PprofAddr != "" {
		runtime.SetMutexProfileFraction(5)
		runtime.SetBlockProfileRate(10000)
		go func() {
			slog.Info("starting pprof server", "addr", cfg.Debug.PprofAddr)
			srv := &http.Server{
				Addr:              cfg.Debug.PprofAddr,
				ReadHeaderTimeout: 5 * time.Second,
			}
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("pprof server failed", "error", err)
			}
		}()
	}

	prometheus.Unregister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	prometheus.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{
		Namespace: "webrtc_grabber",
	}))

	promHandler := promhttp.Handler()
	app.Get("/metrics", func(c *fiber.Ctx) error {
		fasthttpadaptor.NewFastHTTPHandler(promHandler)(c.Context())
		return nil
	})

	// Static assets are embedded in the binary (see package asset), so no asset
	// directory needs to be shipped. The page entrypoints "/", "/login",
	// "/admin", "/capture" and "/player" are served by setupPageRoutes (registered
	// earlier, so they win and apply the auth guards).
	//
	// Block direct access to the raw .html files: otherwise "/capture.html" would
	// bypass the "/capture" guard. HTML is only reachable through the auth-aware
	// routes; this catch-all serves the js (and any other) assets they reference.
	app.Use("/", func(c *fiber.Ctx) error {
		if strings.HasSuffix(c.Path(), ".html") {
			return fiber.ErrNotFound
		}
		return c.Next()
	})
	app.Use("/", filesystem.New(filesystem.Config{
		Root: http.FS(asset.FS),
	}))

	go func() {
		<-ctx.Done()
		slog.Info("shutdown signal received")
		if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
		}
	}()

	slog.Info("starting server", "host", cfg.Server.Host, "port", cfg.Server.Port)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	if cfg.Security.TLSCrtFile != nil && cfg.Security.TLSKeyFile != nil {
		slog.Info("running TLS http server")
		if err := app.ListenTLS(addr, *cfg.Security.TLSCrtFile, *cfg.Security.TLSKeyFile); err != nil {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	} else {
		if err := app.Listen(addr); err != nil {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}
}
