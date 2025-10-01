package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/minasoft-technology/gordion-relay/internal/relay"
)

func main() {
	var (
		configFile = flag.String("config", "config.json", "Path to configuration file")
		debug      = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()

	// Setup logging
	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}

	// Check environment for log level override
	if envLevel := os.Getenv("GORDION_RELAY_LOG_LEVEL"); envLevel != "" {
		switch strings.ToLower(envLevel) {
		case "debug":
			logLevel = slog.LevelDebug
		case "info":
			logLevel = slog.LevelInfo
		case "warn":
			logLevel = slog.LevelWarn
		case "error":
			logLevel = slog.LevelError
		}
	}

	// Check if JSON logging is requested (for containers)
	var handler slog.Handler
	if os.Getenv("GORDION_RELAY_LOG_FORMAT") == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     logLevel,
			AddSource: *debug,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:     logLevel,
			AddSource: *debug,
		})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	slog.Info("Starting Gordion Relay Server")

	// Load configuration
	cfg, err := relay.LoadConfig(*configFile)
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Create relay server based on mode
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var server interface {
		Start(context.Context) error
		Stop()
	}

	switch cfg.Mode {
	case "grpc":
		slog.Info("Starting gRPC relay server mode")
		server = relay.NewGRPCServer(cfg, logger)
	case "websocket":
		slog.Info("Starting WebSocket relay server mode")
		server = relay.NewWebSocketServer(cfg, logger)
	default:
		slog.Error("Unknown relay mode", "mode", cfg.Mode)
		os.Exit(1)
	}

	if err := server.Start(ctx); err != nil {
		slog.Error("Failed to start relay server", "error", err, "mode", cfg.Mode)
		os.Exit(1)
	}

	slog.Info("Relay server started successfully", "mode", cfg.Mode)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutdown signal received, stopping server...")
	server.Stop()
	slog.Info("Relay server stopped")
}