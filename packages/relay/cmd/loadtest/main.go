// Command loadtest spins up N publisher bots and M subscriber bots per
// publisher against a running relay signalling server, generating synthetic
// VP8 RTP traffic so the SFU's hot path can be profiled with pprof.
//
// Run the relay with pprof enabled (default config has it on 127.0.0.1:6060),
// then in another shell:
//
//	go run ./cmd/loadtest -relay ws://127.0.0.1:13478 -publishers 4 -subs-per-publisher 8 -duration 60s
//	go tool pprof -http=:8080 http://127.0.0.1:6060/debug/pprof/profile?seconds=30
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	relayURL         = flag.String("relay", "ws://127.0.0.1:13478", "relay base URL (http(s):// or ws(s)://)")
	credential       = flag.String("credential", "live", "player credential")
	numPublishers    = flag.Int("publishers", 4, "number of publisher bots")
	subsPerPublisher = flag.Int("subs-per-publisher", 8, "subscribers per publisher")
	streamType       = flag.String("stream-type", "desktop", "stream type — desktop has video only, webcam adds audio")
	duration         = flag.Duration("duration", 60*time.Second, "test duration (0 = run until interrupted)")
	videoKbps        = flag.Int("video-kbps", 500, "synthetic video bitrate per publisher (kbps)")
	fps              = flag.Int("fps", 30, "synthetic video fps")
	rampDelay        = flag.Duration("ramp", 25*time.Millisecond, "delay between subscriber starts to spread connection setup")
	statsInterval    = flag.Duration("stats", 5*time.Second, "stats print interval")
	verbose          = flag.Bool("v", false, "verbose logging")
)

type counters struct {
	pubsReady atomic.Int64
	subsReady atomic.Int64
	rxPackets atomic.Int64
	rxBytes   atomic.Int64
	txPackets atomic.Int64
	txBytes   atomic.Int64
}

var stats counters

func main() {
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	wsBase := normalizeWS(*relayURL)
	slog.Info("loadtest starting",
		"relay", wsBase,
		"publishers", *numPublishers,
		"subsPerPublisher", *subsPerPublisher,
		"streamType", *streamType,
		"videoKbps", *videoKbps,
		"fps", *fps,
		"duration", *duration,
	)

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *duration > 0 {
		var stopT context.CancelFunc
		rootCtx, stopT = context.WithTimeout(rootCtx, *duration)
		defer stopT()
	}

	var wg sync.WaitGroup

	pubReady := make(chan string, *numPublishers)
	for i := 0; i < *numPublishers; i++ {
		peerName := fmt.Sprintf("loadbot-%d", i)
		wg.Go(func() {
			err := runPublisher(rootCtx, wsBase, peerName, pubReady)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				slog.Error("publisher exited", "peer", peerName, "error", err)
			}
		})
	}

	readyPeers, ok := waitForPublishers(rootCtx, pubReady, *numPublishers)
	if !ok {
		cancel()
		wg.Wait()
		return
	}
	slog.Info("publishers registered, starting subscribers", "count", len(readyPeers))

	go printStatsLoop(rootCtx)

	totalSubs := *numPublishers * *subsPerPublisher
	for i := 0; i < totalSubs; i++ {
		select {
		case <-rootCtx.Done():
			i = totalSubs
			continue
		default:
		}
		peerName := readyPeers[i%len(readyPeers)]
		idx := i
		wg.Go(func() {
			err := runSubscriber(rootCtx, wsBase, idx, peerName)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				slog.Debug("subscriber exited", "id", idx, "peer", peerName, "error", err)
			}
		})
		if *rampDelay > 0 {
			select {
			case <-time.After(*rampDelay):
			case <-rootCtx.Done():
			}
		}
	}

	wg.Wait()
	printStatsOnce("final")
}

func waitForPublishers(ctx context.Context, ready <-chan string, want int) ([]string, bool) {
	peers := make([]string, 0, want)
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for len(peers) < want {
		select {
		case name := <-ready:
			peers = append(peers, name)
		case <-deadline.C:
			slog.Error("timeout waiting for publishers", "ready", len(peers), "expected", want)
			return peers, false
		case <-ctx.Done():
			return peers, false
		}
	}
	return peers, true
}

func normalizeWS(u string) string {
	switch {
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	default:
		return strings.TrimRight(u, "/")
	}
}

func printStatsLoop(ctx context.Context) {
	t := time.NewTicker(*statsInterval)
	defer t.Stop()
	prev := snapshot{}
	prev.t = time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			cur := snapshot{
				t:       now,
				rxPkts:  stats.rxPackets.Load(),
				rxBytes: stats.rxBytes.Load(),
				txPkts:  stats.txPackets.Load(),
				txBytes: stats.txBytes.Load(),
			}
			dt := cur.t.Sub(prev.t).Seconds()
			if dt <= 0 {
				dt = 1
			}
			slog.Info("stats",
				"pubs", stats.pubsReady.Load(),
				"subs", stats.subsReady.Load(),
				"rx_pps", int64(float64(cur.rxPkts-prev.rxPkts)/dt),
				"rx_mbps", fmt.Sprintf("%.2f", float64(cur.rxBytes-prev.rxBytes)*8/1e6/dt),
				"tx_pps", int64(float64(cur.txPkts-prev.txPkts)/dt),
				"tx_mbps", fmt.Sprintf("%.2f", float64(cur.txBytes-prev.txBytes)*8/1e6/dt),
			)
			prev = cur
		}
	}
}

func printStatsOnce(tag string) {
	slog.Info("stats "+tag,
		"pubs", stats.pubsReady.Load(),
		"subs", stats.subsReady.Load(),
		"rx_pkts", stats.rxPackets.Load(),
		"rx_bytes", stats.rxBytes.Load(),
		"tx_pkts", stats.txPackets.Load(),
		"tx_bytes", stats.txBytes.Load(),
	)
}

type snapshot struct {
	t       time.Time
	rxPkts  int64
	rxBytes int64
	txPkts  int64
	txBytes int64
}
