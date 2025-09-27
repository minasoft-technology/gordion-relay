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

	// Create and start relay server
	server := relay.NewServer(cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		slog.Error("Failed to start relay server", "error", err)
		os.Exit(1)
	}

	slog.Info("Relay server started successfully")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutdown signal received, stopping server...")
	server.Stop()
	slog.Info("Relay server stopped")
}