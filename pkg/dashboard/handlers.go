package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

// secretFields are JSON keys whose values should be masked in API responses.
var secretFields = map[string]bool{
	"api_key":              true,
	"token":               true,
	"app_secret":          true,
	"encrypt_key":         true,
	"verification_token":  true,
	"bot_token":           true,
	"app_token":           true,
	"channel_secret":      true,
	"channel_access_token": true,
	"client_secret":       true,
	"access_token":        true,
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	gwStatus := s.gateway.GetStatus()
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"gateway": gwStatus.State,
	})
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(s.configPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Marshal to generic map so we can mask secrets
	data, _ := json.Marshal(cfg)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	maskSecrets(raw)
	writeJSON(w, http.StatusOK, raw)
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}

	var incoming map[string]any
	if err := json.Unmarshal(body, &incoming); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	// Extract control field
	restartGateway := false
	if v, ok := incoming["_restartGateway"]; ok {
		if b, ok := v.(bool); ok {
			restartGateway = b
		}
		delete(incoming, "_restartGateway")
	}

	// Load existing config to preserve masked secrets
	existing, err := config.LoadConfig(s.configPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	existingData, _ := json.Marshal(existing)
	var existingRaw map[string]any
	json.Unmarshal(existingData, &existingRaw)

	// Merge: keep existing secret values when incoming has masked placeholders
	mergeSecrets(incoming, existingRaw)

	// Unmarshal merged data into a Config struct and save
	merged, _ := json.Marshal(incoming)
	var newCfg config.Config
	if err := json.Unmarshal(merged, &newCfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid config: " + err.Error()})
		return
	}

	if err := config.SaveConfig(s.configPath, &newCfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if restartGateway {
		go s.gateway.Restart()
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarting": restartGateway})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.LoadConfig(s.configPath)

	// Model list status
	models := []map[string]any{}
	if cfg != nil {
		for _, m := range cfg.ModelList {
			models = append(models, map[string]any{
				"model_name": m.ModelName,
				"model":      m.Model,
				"configured": m.APIKey != "",
			})
		}
	}

	// Channel status
	channels := map[string]map[string]bool{}
	if cfg != nil {
		chData, _ := json.Marshal(cfg.Channels)
		var chRaw map[string]map[string]any
		json.Unmarshal(chData, &chRaw)
		for name, ch := range chRaw {
			enabled, _ := ch["enabled"].(bool)
			channels[name] = map[string]bool{"enabled": enabled}
		}
	}

	// Cron jobs
	cronJobs := []any{}
	if cfg != nil {
		cronDir := filepath.Join(filepath.Dir(s.configPath), "cron")
		entries, err := os.ReadDir(cronDir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					data, err := os.ReadFile(filepath.Join(cronDir, e.Name()))
					if err == nil {
						var job any
						if json.Unmarshal(data, &job) == nil {
							cronJobs = append(cronJobs, job)
						}
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"gateway":  s.gateway.GetStatus(),
		"models":   models,
		"channels": channels,
		"cron":      map[string]any{"count": len(cronJobs), "jobs": cronJobs},
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"lines": s.gateway.GetLogs(),
	})
}

func (s *Server) handleGatewayStart(w http.ResponseWriter, r *http.Request) {
	go s.gateway.Start()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGatewayStop(w http.ResponseWriter, r *http.Request) {
	go s.gateway.Stop()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGatewayRestart(w http.ResponseWriter, r *http.Request) {
	go s.gateway.Restart()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// maskSecrets replaces secret field values with truncated versions.
func maskSecrets(data map[string]any) {
	for k, v := range data {
		switch val := v.(type) {
		case map[string]any:
			maskSecrets(val)
		case []any:
			for _, item := range val {
				if m, ok := item.(map[string]any); ok {
					maskSecrets(m)
				}
			}
		case string:
			if secretFields[k] && val != "" {
				if len(val) > 8 {
					data[k] = val[:8] + "***"
				} else {
					data[k] = "***"
				}
			}
		}
	}
}

// mergeSecrets preserves existing secret values when incoming values are masked.
func mergeSecrets(incoming, existing map[string]any) {
	for k, v := range incoming {
		switch val := v.(type) {
		case map[string]any:
			if existMap, ok := existing[k].(map[string]any); ok {
				mergeSecrets(val, existMap)
			}
		case []any:
			// Handle arrays (e.g. model_list) — merge secrets by index
			if existArr, ok := existing[k].([]any); ok {
				for i, item := range val {
					if i < len(existArr) {
						if itemMap, ok := item.(map[string]any); ok {
							if existMap, ok := existArr[i].(map[string]any); ok {
								mergeSecrets(itemMap, existMap)
							}
						}
					}
				}
			}
		case string:
			if secretFields[k] && (strings.HasSuffix(val, "***") || val == "") {
				if existVal, ok := existing[k].(string); ok {
					incoming[k] = existVal
				}
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
