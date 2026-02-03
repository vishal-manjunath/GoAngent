package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// ================= CONFIG & TYPES =================

type Config struct {
	Server *ServerConfig        `yaml:"server,omitempty"`
	Apps   map[string]AppConfig `yaml:"apps"`
}

type ServerConfig struct {
	Addr         string `yaml:"addr,omitempty"`
	DefaultLines int    `yaml:"default_lines,omitempty"`
	MaxLines     int    `yaml:"max_lines,omitempty"`
}

type AppConfig struct {
	Logs map[string]LogTarget `yaml:"logs"`
}

type LogTarget struct {
	Type    string `yaml:"type"`
	Path    string `yaml:"path,omitempty"`
	URL     string `yaml:"url,omitempty"`
	Service string `yaml:"service"`
}

var globalConfig *Config

// ================= LOG SOURCE INTERFACE =================

type LogSource interface {
	ReadLogs(ctx context.Context, lines int) (string, error)
}

type FileLogSource struct{ Path string }

func (f *FileLogSource) ReadLogs(ctx context.Context, lines int) (string, error) {
	content, err := readFileAutoUTF(f.Path)
	if err != nil {
		return "", err
	}
	linesArr := strings.Split(content, "\n")
	if len(linesArr) > lines {
		linesArr = linesArr[len(linesArr)-lines:]
	}
	return strings.Join(linesArr, "\n"), nil
}

// ================= SPECIFICATION TYPES =================

type GitConfig struct {
	UserName  string `json:"user_name"`
	UserEmail string `json:"user_email"`
}

type FlushMetadata struct {
	Reason    string `json:"reason"`
	LogCount  int    `json:"log_count"`
	FlushedAt string `json:"flushed_at"`
}

type AnalyzeResponse struct {
	Bundle AnalyzeBundle `json:"bundle"`
	UseRag bool          `json:"use_rag"`
	TopK   int           `json:"top_k"`
}

type AnalyzeBundle struct {
	ID                   string              `json:"id"`
	WindowStart          string              `json:"windowStart"`
	WindowEnd            string              `json:"windowEnd"`
	RootService          string              `json:"rootService"`
	AffectedServices     []string            `json:"affectedServices"`
	LogPatterns          []AnalyzeLogPattern `json:"logPatterns"`
	Events               []string            `json:"events"`
	Metrics              AnalyzeMetrics      `json:"metrics"`
	DependencyGraph      []string            `json:"dependencyGraph"`
	DerivedRootCauseHint string              `json:"derivedRootCauseHint"`
	GitConfig            *GitConfig          `json:"git_config,omitempty"`
	FlushMetadata        *FlushMetadata      `json:"flush_metadata,omitempty"`
}

type AnalyzeLogPattern struct {
	Pattern         string      `json:"pattern"`
	Count           int         `json:"count"`
	FirstOccurrence string      `json:"firstOccurrence"`
	LastOccurrence  string      `json:"lastOccurrence"`
	ErrorClass      *string     `json:"errorClass"` // Matches "INFO", "WARN", etc.
	LogSource       LogMetadata `json:"logSource"`
}

type AnalyzeMetrics struct {
	CPUZ       float64 `json:"cpuZ"`
	MemZ       float64 `json:"memZ"`
	LatencyZ   float64 `json:"latencyZ"`
	ErrorRateZ float64 `json:"errorRateZ"`
}

// ================= HANDLERS =================

// In main.go

func preprocessHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status      string `json:"status"`
		FlushReason string `json:"flush_reason"`
		Bundle      struct {
			ID               string              `json:"id"`
			WindowStart      string              `json:"windowStart"`
			WindowEnd        string              `json:"windowEnd"`
			RootService      string              `json:"rootService"`
			AffectedServices []string            `json:"affectedServices"`
			LogPatterns      []AnalyzeLogPattern `json:"logPatterns"`
			FlushMetadata    *FlushMetadata      `json:"flush_metadata"`
		} `json:"bundle"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	miner := &LogPatternMinerGo{}

	// Apply the Bundle Builder rules to each pattern in the incoming request
	for i := range input.Bundle.LogPatterns {
		input.Bundle.LogPatterns[i].Pattern = miner.NormalizeMessage(input.Bundle.LogPatterns[i].Pattern)
	}

	// Build the response
	resp := AnalyzeResponse{
		Bundle: AnalyzeBundle{
			ID:               input.Bundle.ID,
			WindowStart:      input.Bundle.WindowStart,
			WindowEnd:        input.Bundle.WindowEnd,
			RootService:      input.Bundle.RootService,
			AffectedServices: input.Bundle.AffectedServices,
			LogPatterns:      input.Bundle.LogPatterns,
			Metrics: AnalyzeMetrics{
				CPUZ:       1.5, // Standard mock value
				ErrorRateZ: float64(len(input.Bundle.LogPatterns)),
			},
			FlushMetadata: input.Bundle.FlushMetadata,
		},
		UseRag: true,
		TopK:   5,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Helper to calculate error rate from patterns for the response
func calculateErrorRate(patterns []AnalyzeLogPattern) float64 {
	errs := 0.0
	for _, p := range patterns {
		if p.ErrorClass != nil && strings.ToUpper(*p.ErrorClass) == "ERROR" {
			errs += float64(p.Count)
		}
	}
	return errs
}

// ================= HELPERS =================

func readFileAutoUTF(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, _ := io.ReadAll(f)
	return string(raw), nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "bind address")
	flag.Parse()

	http.HandleFunc("/logs/preprocess", preprocessHandler)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) })

	fmt.Println("[OPSCURE] Agent starting on", *addr)
	http.ListenAndServe(*addr, nil)
}
