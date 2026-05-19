package server

import (
	"context"
	"crypto/tls"
	"log/slog"
)

type App struct {
	Config *Config
	Server *TunnelServer
}

func NewApp(ctx context.Context) (*App, error) {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	routeMgr, err := buildRouteManager(cfg)
	if err != nil {
		return nil, err
	}

	var tlsCfg *tls.Config
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		tlsCfg, err = loadTLSConfig(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, err
		}
		slog.Info("tls termination enabled")
	}

	slog.Info("tunnel server configured",
		"tcp", cfg.Listen,
		"ws", cfg.WSListen,
	)

	return &App{
		Config: cfg,
		Server: New(cfg, routeMgr, tlsCfg),
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.Server.Start(ctx)
}
