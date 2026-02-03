package main

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type LogMetadata struct {
	Type      string `json:"type,omitempty"`
	File      string `json:"file,omitempty"`
	Container string `json:"container,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Node      string `json:"node,omitempty"`
}

type RawLogGo struct {
	Timestamp  string
	Level      string
	Service    string
	Message    string
	LogSource  LogMetadata
	ErrorClass *string
}

type LogPatternGo struct {
	Pattern         string      `json:"pattern"`
	Count           int         `json:"count"`
	FirstOccurrence string      `json:"firstOccurrence"`
	LastOccurrence  string      `json:"lastOccurrence"`
	ErrorClass      *string     `json:"errorClass"`
	LogSource       LogMetadata `json:"logSource"`
}

type MetricsGo struct {
	CPUZ       float64 `json:"cpuZ"`
	MemZ       float64 `json:"memZ"`
	LatencyZ   float64 `json:"latencyZ"`
	ErrorRateZ float64 `json:"errorRateZ"`
}

type CorrelationBundleGo struct {
	ID                   string         `json:"id"`
	WindowStart          string         `json:"windowStart"`
	WindowEnd            string         `json:"windowEnd"`
	RootService          *string        `json:"rootService"`
	AffectedServices     []string       `json:"affectedServices"`
	LogPatterns          []LogPatternGo `json:"logPatterns"`
	Events               []string       `json:"events"`
	Metrics              MetricsGo      `json:"metrics"`
	DependencyGraph      []string       `json:"dependencyGraph"`
	DerivedRootCauseHint string         `json:"derivedRootCauseHint"`
}

type LogParserGo struct{}

func (p *LogParserGo) ParseLogs(rawData []map[string]interface{}) ([]RawLogGo, error) {
	if rawData == nil {
		return nil, errors.New("rawData is nil")
	}

	var parsed []RawLogGo
	for _, entry := range rawData {
		ts := time.Now().UTC().Format(time.RFC3339)
		if v, ok := entry["timestamp"].(string); ok && v != "" {
			ts = v
		}

		level := "INFO"
		if v, ok := entry["severity"].(string); ok {
			level = strings.ToUpper(v)
		} else if v, ok := entry["level"].(string); ok {
			level = strings.ToUpper(v)
		}

		msg, _ := entry["message"].(string)

		var meta LogMetadata
		if src, ok := entry["source"].(map[string]interface{}); ok {
			meta.Container, _ = src["container"].(string)
			meta.Namespace, _ = src["namespace"].(string)
			meta.File, _ = src["file"].(string)
			meta.Type = "application"
		}

		var ec *string
		if level == "ERROR" {
			s := "Error"
			ec = &s
		}

		parsed = append(parsed, RawLogGo{
			Timestamp:  ts,
			Level:      level,
			Message:    msg,
			LogSource:  meta,
			ErrorClass: ec,
		})
	}

	sort.Slice(parsed, func(i, j int) bool {
		return parsed[i].Timestamp < parsed[j].Timestamp
	})
	return parsed, nil
}

type LogPatternMinerGo struct{}

func (m *LogPatternMinerGo) NormalizeMessage(msg string) string {
	result := msg
	result = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`).ReplaceAllString(result, "<UUID>")
	result = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`).ReplaceAllString(result, "<IP>")
	result = regexp.MustCompile(`(?i)(id|user_?id|order_?id|session_?id)[=:\s]+[a-zA-Z0-9_-]+`).ReplaceAllString(result, "id=<ID>")
	result = regexp.MustCompile(`\b\d{6,}\b`).ReplaceAllString(result, "<NUM>")
	return result
}

func (m *LogPatternMinerGo) MinePatterns(logs []RawLogGo) []LogPatternGo {
	patterns := make(map[string]*LogPatternGo)

	for _, log := range logs {
		normalized := m.NormalizeMessage(log.Message)
		hash := md5.Sum([]byte(normalized))
		key := hex.EncodeToString(hash[:])

		if p, exists := patterns[key]; exists {
			p.Count++
			p.LastOccurrence = log.Timestamp
		} else {
			patterns[key] = &LogPatternGo{
				Pattern:         normalized,
				Count:           1,
				FirstOccurrence: log.Timestamp,
				LastOccurrence:  log.Timestamp,
				ErrorClass:      log.ErrorClass,
				LogSource:       log.LogSource,
			}
		}
	}

	var out []LogPatternGo
	for _, p := range patterns {
		out = append(out, *p)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Count > out[j].Count
	})
	return out
}

type BundleFactoryGo struct{}

func (f *BundleFactoryGo) CreateBundle(logs []RawLogGo, patterns []LogPatternGo, rootService string) (*CorrelationBundleGo, error) {
	if len(logs) == 0 {
		return nil, errors.New("empty logs")
	}

	serviceSet := map[string]struct{}{}
	errorCount := 0
	for _, l := range logs {
		if l.LogSource.Container != "" {
			base := regexp.MustCompile(`-[a-f0-9]+-[a-z0-9]+$`).ReplaceAllString(l.LogSource.Container, "")
			serviceSet[base] = struct{}{}
		}
		if l.Level == "ERROR" {
			errorCount++
		}
	}

	var services []string
	for s := range serviceSet {
		services = append(services, s)
	}

	return &CorrelationBundleGo{
		ID:               fmt.Sprintf("bundle_%d", time.Now().Unix()),
		RootService:      &rootService,
		WindowStart:      logs[0].Timestamp,
		WindowEnd:        logs[len(logs)-1].Timestamp,
		AffectedServices: services,
		LogPatterns:      patterns,
		Metrics: MetricsGo{
			CPUZ:       float64(len(logs)) * 0.2,
			ErrorRateZ: float64(errorCount),
			LatencyZ:   0.5,
		},
		DependencyGraph:      services,
		DerivedRootCauseHint: "Identified via log pattern clustering",
	}, nil
}

type LogPreprocessorFullGo struct {
	Parser  *LogParserGo
	Miner   *LogPatternMinerGo
	Factory *BundleFactoryGo
}

func NewLogPreprocessorFullGo() *LogPreprocessorFullGo {
	return &LogPreprocessorFullGo{
		Parser:  &LogParserGo{},
		Miner:   &LogPatternMinerGo{},
		Factory: &BundleFactoryGo{},
	}
}

func (p *LogPreprocessorFullGo) Process(rawData []map[string]interface{}, rootService string) (*CorrelationBundleGo, error) {
	logs, err := p.Parser.ParseLogs(rawData)
	if err != nil {
		return nil, err
	}
	patterns := p.Miner.MinePatterns(logs)
	return p.Factory.CreateBundle(logs, patterns, rootService)
}
