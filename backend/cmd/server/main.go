package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"streamapp/backend/internal/database"
	"streamapp/backend/internal/handlers"
)

// We no longer embed the frontend since it's a separate Flutter app

func main() {
	// Initialize SQLite database
	db, err := database.InitDB("streamapp.db")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	r := chi.NewRouter()

	// Standard tools for server stability and logs
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Allows the frontend and backend to communicate seamlessly
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{
			"http://localhost:5173",
			"http://localhost:8080",
			"http://localhost:8081",
			"http://127.0.0.1:8081",
			"http://192.168.4.143:8081",
			"http://dev-server:8081",
		},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// API endpoints go here
	r.Route("/api", func(api chi.Router) {
		api.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status": "healthy", "service": "streamapp backend"}`))
		})
		
		// Speed Test (Used by Flutter for Auto Quality)
		api.Get("/speedtest", handlers.SpeedTest)

		// Playlists
		api.Get("/playlists", handlers.GetPlaylists)
		api.Post("/playlists", handlers.AddPlaylist)
		api.Delete("/playlists/{id}", handlers.DeletePlaylist)
		api.Post("/playlists/{id}/sync", handlers.SyncPlaylist)

		// Channel Groups / Categories
		api.Get("/groups", handlers.GetGroups)

		// Channels
		api.Get("/channels", handlers.GetChannels)

		// EPG
		api.Get("/epg/live", handlers.GetLiveEPG)
		api.Post("/epg/sync", handlers.SyncEPGHandler)

		// Streaming Endpoints
		api.Get("/streams/play", handlers.PlayStream)
		api.Get("/streams/start", handlers.StartHLSStream)
		api.Get("/streams/stop", handlers.StopHLSStream)
		api.Get("/streams/stop_all", handlers.StopAllStreams)
		api.Get("/streams/hls/{id}/*", handlers.ServeHLSSegments)
	})

	// Fallback route for undefined endpoints
	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("404 Not Found"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Channel to listen for errors coming from the listener.
	serverErrors := make(chan error, 1)

	// Start the server
	go func() {
		log.Printf("🚀 Server booting up cleanly on port %s...", port)
		serverErrors <- srv.ListenAndServe()
	}()

	// Channel to listen for an interrupt or terminate signal from the OS.
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)

	// Block until a signal is received or an error occurs
	select {
	case err := <-serverErrors:
		log.Fatalf("Server crash detected: %v", err)

	case sig := <-osSignals:
		log.Printf("Shutdown signal received: %v", sig)

		// Ask the server to cleanly shut down
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Graceful shutdown did not complete in 5s: %v", err)
		}

		// Ensure all transcoding processes are killed so tuners are released
		log.Printf("Killing all active streams...")
		handlers.ShutdownAllStreams()
		log.Printf("Cleanup complete, exiting.")
	}
}
