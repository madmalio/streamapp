package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"streamapp/backend/internal/database"
	"streamapp/backend/internal/handlers"
)

// This tells Go to pull the compiled frontend files directly into the server binary
//go:embed all:dist
var frontendFS embed.FS

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
		AllowedOrigins:   []string{"http://localhost:5173", "http://localhost:8080"},
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

		// Streaming Proxy
		api.Get("/streams/proxy", handlers.ProxyStream)
		api.Get("/streams/start", handlers.StartHLSStream)
		api.Get("/streams/hls/{id}/*", handlers.ServeHLSSegments)
	})

	// Static routing rules to serve the embedded Vite interface
	publicFS, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		log.Fatalf("Failed to initialize embedded frontend filesystem: %v", err)
	}

	fileServer := http.FileServer(http.FS(publicFS))
	
	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, ".") {
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
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