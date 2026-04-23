package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"
)

// ── Config ────────────────────────────────────────────────────────────────────

type Config struct {
	ListenAddr   string // LISTEN_ADDR   (default: :8080)
	NtfyURL      string // NTFY_URL      (default: https://ntfy.sh)
	NtfyTopic    string // NTFY_TOPIC    (required)
	NtfyAuth     string // NTFY_AUTH     e.g. "Bearer <token>" or "Basic <b64>" (optional)
	NtfyPriority string // NTFY_PRIORITY (default: default)
	TemplatePath string // TEMPLATE_PATH (default: templates/default.tmpl)
}

func loadConfig() Config {
	cfg := Config{
		ListenAddr:   envOr("LISTEN_ADDR", ":8080"),
		NtfyURL:      envOr("NTFY_URL", "https://ntfy.sh"),
		NtfyTopic:    envOr("NTFY_TOPIC", ""),
		NtfyAuth:     envOr("NTFY_AUTH", ""),
		NtfyPriority: envOr("NTFY_PRIORITY", "default"),
		TemplatePath: envOr("TEMPLATE_PATH", "templates/default.tmpl"),
	}
	if cfg.NtfyTopic == "" {
		log.Fatal("FATAL: NTFY_TOPIC environment variable is required")
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── Alert payload ─────────────────────────────────────────────────────────────

// TemplateData is passed to the message template.
// Known fields are promoted to typed struct fields; every raw JSON key is also
// accessible via {{.All.some_key}} for full flexibility.
type TemplateData struct {
	RuleName string
	Message  string
	Severity string
	Index    string
	NumHits  interface{} // may arrive as number or string
	All      map[string]interface{}
}

func parseAlert(body []byte) (TemplateData, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return TemplateData{}, fmt.Errorf("invalid JSON: %w", err)
	}

	td := TemplateData{
		RuleName: stringField(raw, "rule_name"),
		Message:  stringField(raw, "message"),
		Severity: stringField(raw, "severity"),
		Index:    stringField(raw, "index"),
		NumHits:  raw["num_hits"],
		All:      raw,
	}
	return td, nil
}

func stringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// ── ntfy priority mapping ─────────────────────────────────────────────────────

var severityPriority = map[string]string{
	"critical": "high",
	"warning":  "default",
	"info":     "low",
}

func ntfyPriority(defaultPriority, severity string) string {
	if p, ok := severityPriority[severity]; ok {
		return p
	}
	return defaultPriority
}

// ── Server ────────────────────────────────────────────────────────────────────

type Server struct {
	cfg    Config
	tmpl   *template.Template
	client *http.Client
}

func NewServer(cfg Config) (*Server, error) {
	funcMap := template.FuncMap{
		"toUpper": strings.ToUpper,
		"toLower": strings.ToLower,
	}
	tmpl, err := template.New("default.tmpl").Funcs(funcMap).ParseFiles(cfg.TemplatePath)
	if err != nil {
		return nil, fmt.Errorf("loading template %q: %w", cfg.TemplatePath, err)
	}
	return &Server{
		cfg:  cfg,
		tmpl: tmpl,
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// POST /webhook
func (s *Server) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB cap
	if err != nil {
		log.Printf("ERROR reading body: %v", err)
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	alert, err := parseAlert(body)
	if err != nil {
		log.Printf("ERROR parsing alert: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("INFO  alert received rule=%q severity=%q num_hits=%v",
		alert.RuleName, alert.Severity, alert.NumHits)

	var buf bytes.Buffer
	if err := s.tmpl.Execute(&buf, alert); err != nil {
		log.Printf("ERROR rendering template: %v", err)
		http.Error(w, "template render error", http.StatusInternalServerError)
		return
	}

	if err := s.sendNtfy(buf.String(), alert); err != nil {
		log.Printf("ERROR forwarding to ntfy: %v", err)
		http.Error(w, "failed to forward alert", http.StatusBadGateway)
		return
	}

	log.Printf("INFO  alert forwarded  topic=%q", s.cfg.NtfyTopic)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, `{"status":"ok"}`)
}

// GET /health
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, `{"status":"ok"}`)
}

func (s *Server) sendNtfy(message string, alert TemplateData) error {
	url := fmt.Sprintf("%s/%s", s.cfg.NtfyURL, s.cfg.NtfyTopic)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(message))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	title := alert.RuleName
	if title == "" {
		title = "ElastAlert"
	}
	tags := "rotating_light"
	if alert.Severity != "" {
		tags = fmt.Sprintf("rotating_light,%s", alert.Severity)
	}

	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("X-Title", title)
	req.Header.Set("X-Priority", ntfyPriority(s.cfg.NtfyPriority, alert.Severity))
	req.Header.Set("X-Tags", tags)
	if s.cfg.NtfyAuth != "" {
		auth := s.cfg.NtfyAuth
		if !strings.Contains(auth, " ") {
			auth = "Bearer " + auth
		}
		req.Header.Set("Authorization", auth)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending to ntfy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ntfy responded %d: %s", resp.StatusCode, string(rb))
	}
	return nil
}

// ── Entry point ───────────────────────────────────────────────────────────────

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Self-healthcheck mode: used by Docker HEALTHCHECK CMD
	if len(os.Args) == 2 && os.Args[1] == "-health" {
		resp, err := http.Get("http://localhost:8080/health")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg := loadConfig()

	srv, err := NewServer(cfg)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", srv.webhookHandler)
	mux.HandleFunc("/health", healthHandler)

	log.Printf("INFO  ea-ntfy starting  addr=%s  ntfy=%s/%s  template=%s",
		cfg.ListenAddr, cfg.NtfyURL, cfg.NtfyTopic, cfg.TemplatePath)

	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("FATAL: %v", err)
	}
}
