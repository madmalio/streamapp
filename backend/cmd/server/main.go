package main

import (
	"log"
	"net/http"
	"os"

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

	log.Printf("🚀 Server booting up cleanly on port %s...", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("Server crash detected: %v", err)
	}
}
