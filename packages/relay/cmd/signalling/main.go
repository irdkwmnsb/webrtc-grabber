package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/lmittmann/tint"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"

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

	cfgManager, err := config.NewManager("conf")
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	cfg := cfgManager.Get()

	app := fiber.New(fiber.Config{
		BodyLimit: 50 * 1024 * 1024,
	})

	server, err := signalling.NewServer(&cfg, app)
	if err != nil {
		slog.Error("can not start signalling server", "error", err)
		os.Exit(1)
	}

	defer server.Close()

	cfgManager.SetUpdateCallback(func(newCfg *config.AppConfig) {
		server.UpdateConfig(newCfg)
	})

	server.SetupWebSocketsAndApi()
	app.Use(pprof.New())

	prometheus.Unregister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	prometheus.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{
		Namespace: "webrtc_grabber",
	}))

	promHandler := promhttp.Handler()
	app.Get("/metrics", func(c *fiber.Ctx) error {
		fasthttpadaptor.NewFastHTTPHandler(promHandler)(c.Context())
		return nil
	})

	app.Static("/", "./asset")
	app.Static("/player", "./asset/player.html")
	app.Static("/capture", "./asset/capture.html")

	slog.Info("starting server", "host", cfg.Server.Host, "port", cfg.Server.Port)

	if cfg.Security.TLSCrtFile != nil && cfg.Security.TLSKeyFile != nil {
		slog.Info("running TLS http server")
		if err := app.ListenTLS(cfg.Server.Host+":"+strconv.Itoa(cfg.Server.Port), *cfg.Security.TLSCrtFile, *cfg.Security.TLSKeyFile); err != nil {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	} else {
		if err := app.Listen(cfg.Server.Host + ":" + strconv.Itoa(cfg.Server.Port)); err != nil {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}
}
