// Command kite-kvm is a single-host KVM control node (被控节点). It manages the
// local hypervisor's virtual machines through libvirt and exposes a versioned,
// authenticated REST API so a billing system (WHMCS-style) or panel can
// provision and operate VPS instances.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kite-plus/kite-kvm/internal/api"
	"github.com/kite-plus/kite-kvm/internal/catalog"
	"github.com/kite-plus/kite-kvm/internal/config"
	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/network"
	"github.com/kite-plus/kite-kvm/internal/provision"
	"github.com/kite-plus/kite-kvm/internal/store"
	"github.com/kite-plus/kite-kvm/internal/vm"
)

// jobWorkers is the number of concurrent in-process job workers.
const jobWorkers = 4

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", "configs/kite-kvm.yaml", "path to the config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(*configPath, logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn := libvirt.New(cfg.Libvirt.URI)
	if err := conn.Connect(ctx); err != nil {
		// Non-fatal: start anyway and let /readyz report the outage.
		logger.Warn("libvirt connect failed at startup; /readyz will report unavailable",
			"uri", cfg.Libvirt.URI, "error", err)
	}
	defer func() { _ = conn.Close() }()

	st, err := store.Open(ctx, cfg.Storage.StatePath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	netmgr, err := network.NewManager(cfg, st, conn)
	if err != nil {
		return err
	}
	cat := catalog.New(cfg)
	provisioner := provision.NewProvisioner(conn, cfg.Libvirt.StoragePool, cfg.Libvirt.InstanceDir)

	queue := job.NewQueue(st, jobWorkers, logger)
	vmService := vm.NewService(cfg, st, conn, cat, netmgr, provisioner, queue, logger)
	// The runner is installed by NewService; start the workers now. recover()
	// settles interrupted jobs; then reconcile VMs the crash left mid-flight.
	queue.Start(ctx)
	defer queue.Stop()
	vmService.ReconcileOnStart(ctx)

	// Background loops are tracked so they drain before the store is closed.
	var bg sync.WaitGroup
	bg.Add(2)
	// Periodically sample per-VM transfer and enforce traffic quotas.
	go func() {
		defer bg.Done()
		vmService.AccountTraffic(ctx, time.Duration(cfg.Traffic.IntervalSeconds)*time.Second)
	}()
	// Periodically purge expired idempotency keys so the table stays bounded.
	go func() {
		defer bg.Done()
		sweepIdempotency(ctx, st, logger)
	}()

	router := api.NewRouter(api.Options{
		Logger:    logger,
		Auth:      cfg.Auth,
		Ready:     conn.Ping,
		Catalog:   cat,
		Store:     st,
		VMService: vmService,
		Version:   version,
	})

	srv := api.NewServer(cfg.Server, router, logger)
	logger.Info("starting kite-kvm",
		"version", version,
		"addr", cfg.Server.Addr,
		"insecure", cfg.Server.Insecure,
	)
	err = srv.Run(ctx)
	// Drain the background loops before the deferred store.Close().
	bg.Wait()
	return err
}

// sweepIdempotency periodically removes expired idempotency keys so the table
// stays bounded. It runs until ctx is cancelled.
func sweepIdempotency(ctx context.Context, st store.Store, logger *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := st.DeleteExpiredIdempotency(context.Background())
			if err != nil {
				logger.Warn("idempotency sweep failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("swept expired idempotency keys", "count", n)
			}
		}
	}
}
