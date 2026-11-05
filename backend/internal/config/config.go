package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DBURL          string
	JWTSecret      string
	AdminUser      string
	AdminPassword  string
	Port           string
	CheckTimeout   time.Duration
	AllowedOrigins []string

	// ReportDir is where the periodic uptime report CSV files are written,
	// kept separate from the application tree (default /home/noc/report).
	ReportDir string

	// LLM (Qwen via DashScope OpenAI-compatible endpoint) used for AI
	// root-cause analysis on down events. The feature stays dormant until
	// UPTIME_LLM_API_KEY is set.
	LLMAPIKey  string
	LLMBaseURL string
	LLMModel   string
}

func envList(key, fallback string) []string {
	raw := env(key, fallback)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func Load() *Config {
	timeoutSec, _ := strconv.Atoi(env("UPTIME_CHECK_TIMEOUT_SEC", "10"))
	return &Config{
		// Real credentials come from the environment (systemd on the server).
		// The default here is a placeholder only — never put secrets in code.
		DBURL:          env("UPTIME_DB_URL", "postgres://apps:changeme@127.0.0.1:5432/Monitoring?sslmode=disable"),
		JWTSecret:      env("UPTIME_JWT_SECRET", "dev-secret-change-me"),
		AdminUser:      env("UPTIME_ADMIN_USER", "admin"),
		AdminPassword:  env("UPTIME_ADMIN_PASSWORD", "admin123"),
		Port:           env("UPTIME_PORT", "8080"),
		CheckTimeout:   time.Duration(timeoutSec) * time.Second,
		AllowedOrigins: envList("UPTIME_ALLOWED_ORIGINS", "*"),
		ReportDir:      env("UPTIME_REPORT_DIR", "/home/noc/report"),
		LLMAPIKey:      env("UPTIME_LLM_API_KEY", ""),
		LLMBaseURL:     env("UPTIME_LLM_BASE_URL", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"),
		LLMModel:       env("UPTIME_LLM_MODEL", "qwen-flash"),
	}
}
