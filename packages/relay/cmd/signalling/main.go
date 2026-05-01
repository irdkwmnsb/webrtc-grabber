package main

import (
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
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
