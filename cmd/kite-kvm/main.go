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
	"syscall"

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
	// The runner is installed by NewService; start the workers now.
	queue.Start(ctx)
	defer queue.Stop()

	router := api.NewRouter(api.Options{
		Logger:    logger,
		Auth:      cfg.Auth,
		Ready:     conn.Ping,
		Catalog:   cat,
		Store:     st,
		VMService: vmService,
	})

	srv := api.NewServer(cfg.Server, router, logger)
	logger.Info("starting kite-kvm",
		"version", version,
		"addr", cfg.Server.Addr,
		"insecure", cfg.Server.Insecure,
	)
	return srv.Run(ctx)
}
