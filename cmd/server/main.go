package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"walnut-billing/internal/app/bootstrap"
)

func main() {
	app, err := bootstrap.Build()
	if err != nil {
		slog.Error("Failed to bootstrap walnut Billing Server", "error", err)
		os.Exit(1)
	}
	defer app.Stop()

	srv := app.HTTPServer()
	go func() {
		app.Logger.Info("Server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.Logger.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	app.Logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		app.Logger.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	app.Logger.Info("Server exited cleanly")
}
