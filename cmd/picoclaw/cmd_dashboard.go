package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/sipeed/picoclaw/pkg/dashboard"
)

func dashboardCmd() {
	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	adminUser := os.Getenv("ADMIN_USERNAME")
	if adminUser == "" {
		adminUser = "admin"
	}

	adminPass := os.Getenv("ADMIN_PASSWORD")

	// Parse CLI flags
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--port":
			if i+1 < len(os.Args) {
				if v, err := strconv.Atoi(os.Args[i+1]); err == nil {
					port = v
				}
				i++
			}
		case "--admin-user":
			if i+1 < len(os.Args) {
				adminUser = os.Args[i+1]
				i++
			}
		case "--admin-pass":
			if i+1 < len(os.Args) {
				adminPass = os.Args[i+1]
				i++
			}
		}
	}

	configPath := getConfigPath()

	srv := dashboard.New(configPath, port, adminUser, adminPass)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down dashboard...")
		cancel()
	}()

	if err := srv.Start(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "Dashboard error: %v\n", err)
		os.Exit(1)
	}
}
