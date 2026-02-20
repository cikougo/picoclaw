package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

type Server struct {
	httpServer *http.Server
	gateway    *GatewayManager
	configPath string
	adminUser  string
	adminPass  string
	startTime  time.Time
}

func New(configPath string, port int, adminUser, adminPass string) *Server {
	if adminPass == "" {
		adminPass = generatePassword()
		fmt.Printf("Generated admin password: %s\n", adminPass)
	}

	s := &Server{
		gateway:    NewGatewayManager(),
		configPath: configPath,
		adminUser:  adminUser,
		adminPass:  adminPass,
		startTime:  time.Now(),
	}

	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("/health", s.handleHealth)

	// Protected endpoints
	mux.HandleFunc("/", s.requireAuth(s.handleIndex))
	mux.HandleFunc("/api/config", s.requireAuth(s.handleConfig))
	mux.HandleFunc("/api/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("/api/logs", s.requireAuth(s.handleLogs))
	mux.HandleFunc("/api/gateway/start", s.requireAuth(s.handleGatewayStart))
	mux.HandleFunc("/api/gateway/stop", s.requireAuth(s.handleGatewayStop))
	mux.HandleFunc("/api/gateway/restart", s.requireAuth(s.handleGatewayRestart))
	mux.HandleFunc("/api/workspace/files", s.requireAuth(s.handleWorkspaceFiles))
	mux.HandleFunc("/api/workspace/file", s.requireAuth(s.handleWorkspaceFile))

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	return s
}

func (s *Server) Start(ctx context.Context) error {
	// Auto-start gateway if any provider has an API key
	if s.hasProviderKey() {
		fmt.Println("Provider API key found, auto-starting gateway...")
		go s.gateway.Start()
	} else {
		fmt.Println("No provider API keys configured. Configure via the web UI to start the gateway.")
	}

	fmt.Printf("Dashboard running at http://%s\n", s.httpServer.Addr)
	fmt.Printf("Login: %s / %s\n", s.adminUser, s.adminPass)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return s.Stop(context.Background())
	}
}

func (s *Server) Stop(ctx context.Context) error {
	s.gateway.Stop()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleConfigGet(w, r)
	case http.MethodPut:
		s.handleConfigPut(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(s.adminUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(s.adminPass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="picoclaw"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) hasProviderKey() bool {
	cfg, err := config.LoadConfig(s.configPath)
	if err != nil {
		return false
	}

	// Check legacy providers
	data, _ := json.Marshal(cfg.Providers)
	var provRaw map[string]map[string]any
	if json.Unmarshal(data, &provRaw) == nil {
		for _, prov := range provRaw {
			if key, ok := prov["api_key"].(string); ok && key != "" {
				return true
			}
		}
	}

	// Check model_list
	for _, m := range cfg.ModelList {
		if m.APIKey != "" {
			return true
		}
	}

	return false
}

func generatePassword() string {
	b := make([]byte, 18)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)[:24]
}
