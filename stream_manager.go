package main

import (
	"encoding/json"
	"sync"
	"time"
)

//
// ===================== PLACEHOLDERS / DEPENDENCIES =====================
//

type RawLog struct {
	Data map[string]interface{}
}

type CorrelationBundle struct {
	Sequence []RawLog
	Metadata map[string]interface{}
}

func (c *CorrelationBundle) ModelDumpJSON() string {
	b, _ := json.Marshal(c)
	return string(b)
}

type LogPreprocessor struct {
	Parser  *LogParser
	Factory *BundleFactory
}

func NewLogPreprocessor() *LogPreprocessor {
	return &LogPreprocessor{
		Parser:  &LogParser{},
		Factory: &BundleFactory{},
	}
}

type LogParser struct{}

func (p *LogParser) ParseLogs(logs []map[string]interface{}) ([]RawLog, error) {
	var parsed []RawLog
	for _, l := range logs {
		parsed = append(parsed, RawLog{Data: l})
	}
	return parsed, nil
}

type BundleFactory struct{}

func (f *BundleFactory) CreateBundle(logs []RawLog, patterns []string) *CorrelationBundle {
	return &CorrelationBundle{
		Sequence: logs,
		Metadata: map[string]interface{}{
			"patterns": patterns,
		},
	}
}

//
// ===================== CONFIG & STATS =====================
//

type FlushReason string

const (
	FlushErrorDetected FlushReason = "error_detected"
	FlushTimeElapsed   FlushReason = "time_elapsed"
	FlushManual        FlushReason = "manual"
	FlushBufferFull    FlushReason = "buffer_full"
	FlushShutdown      FlushReason = "shutdown"
	FlushExpired	   FlushReason = "expired"
)

type StreamConfig struct {
	MaxLogsPerSecond       int
	MaxBufferSize          int
	MaxTokensPerRun        int
	MaxStreamDurationMin   float64
	MinStreamDurationMin   float64
	WindowSeconds          int
	FlushIntervalSeconds   int
	ErrorTriggersImmediate bool
}

func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		MaxLogsPerSecond:       50,
		MaxBufferSize:          10,
		MaxTokensPerRun:        6000,
		MaxStreamDurationMin:   0.5,
		MinStreamDurationMin:   15,
		WindowSeconds:          60,
		FlushIntervalSeconds:   30,
		ErrorTriggersImmediate: true,
	}
}

type StreamStats struct {
	StartTime         time.Time
	TotalLogsIngested int
	BufferFlushCount  int
	DroppedLogs       int
}

//
// ===================== STREAM MANAGER =====================
//

type StreamManager struct {
	Config StreamConfig
	Buffer []RawLog
	Stats  StreamStats

	Preprocessor *LogPreprocessor

	checkWindowStart time.Time
	logsInWindow     int

	lastFlushTime time.Time
	stopChan      chan struct{}

	subscribers map[chan *CorrelationBundle]struct{}

	mu sync.Mutex
}

func NewStreamManager(config StreamConfig) *StreamManager {
	sm := &StreamManager{
		Config:           config,
		Buffer:           []RawLog{},
		Stats:            StreamStats{StartTime: time.Now()},
		Preprocessor:     NewLogPreprocessor(),
		checkWindowStart: time.Now(),
		logsInWindow:     0,
		lastFlushTime:    time.Now(),
		stopChan:         make(chan struct{}),
		subscribers:      make(map[chan *CorrelationBundle]struct{}),
	}

	go sm.backgroundFlushLoop()
	return sm
}

//
// ===================== WINDOW PRUNING =====================
//

func (s *StreamManager) pruneOldLogs() {
	cutoff := time.Now().Add(-time.Duration(s.Config.WindowSeconds) * time.Second)

	pruned := s.Buffer[:0]
	for _, log := range s.Buffer {
		if ts, ok := log.Data["timestamp"].(time.Time); ok {
			if ts.After(cutoff) {
				pruned = append(pruned, log)
			}
		} else {
			// keep log if timestamp missing
			pruned = append(pruned, log)
		}
	}
	s.Buffer = pruned
}

//
// ===================== INGEST =====================
//

// In stream_manager.go: Replace the existing Ingest function
func (s *StreamManager) Ingest(logDict map[string]interface{}) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.IsExpired() {
		return false, false
	}

	now := time.Now()
	if now.Sub(s.checkWindowStart) >= time.Second {
		s.checkWindowStart = now
		s.logsInWindow = 0
	}

	if s.logsInWindow >= s.Config.MaxLogsPerSecond {
		s.Stats.DroppedLogs++
		return false, false
	}

	parsed, err := s.Preprocessor.Parser.ParseLogs([]map[string]interface{}{logDict})
	if err != nil {
		s.Stats.DroppedLogs++
		return false, false
	}

	// sliding window prune
	s.pruneOldLogs()

	s.Buffer = append(s.Buffer, parsed[0])
	s.logsInWindow++
	s.Stats.TotalLogsIngested++

	// Check if this specific log is an error
	isError := false
	if s.Config.ErrorTriggersImmediate {
		if lvl, ok := logDict["level"].(string); ok && lvl == "ERROR" {
			isError = true
		}
	}

	return true, isError
}

func (s *StreamManager) ShouldFlush() bool {
	return len(s.Buffer) >= s.Config.MaxBufferSize
}

func (s *StreamManager) IsExpired() bool {
	elapsedMinutes := time.Since(s.Stats.StartTime).Minutes()
	return elapsedMinutes > s.Config.MaxStreamDurationMin
}

//
// ===================== BACKGROUND FLUSH =====================
//

func (s *StreamManager) backgroundFlushLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			// Check for expiration first
			if s.IsExpired() {
				s.FlushWithReason(FlushExpired)
				// You might want to shut down or signal the manager is done here
				return 
			}
			if time.Since(s.lastFlushTime).Seconds() >= float64(s.Config.FlushIntervalSeconds) {
				s.FlushWithReason(FlushTimeElapsed)
			}
		}
	}
}

//
// ===================== FLUSH =====================
//

func (s *StreamManager) FlushWithReason(reason FlushReason) *CorrelationBundle {
	s.mu.Lock()

	if len(s.Buffer) == 0 {
		s.mu.Unlock()
		return nil
	}

	patterns := deriveStreamPatterns(s.Buffer)
	bundle := s.Preprocessor.Factory.CreateBundle(s.Buffer, patterns)

	bundle.Metadata["flush_reason"] = string(reason)
	bundle.Metadata["flushed_at"] = time.Now().UTC().Format(time.RFC3339)

	estimatedTokens := s.estimateTokens(bundle)
	bundle.Metadata["original_token_est"] = estimatedTokens

	s.Buffer = []RawLog{}
	s.Stats.BufferFlushCount++
	s.lastFlushTime = time.Now()

	s.mu.Unlock() // 🔓 unlock BEFORE publish

	s.publish(bundle)
	return bundle
}

func (s *StreamManager) Flush() *CorrelationBundle {
	return s.FlushWithReason(FlushManual)
}

func (s *StreamManager) Shutdown() {
	close(s.stopChan)
	s.FlushWithReason(FlushShutdown)
}

func (s *StreamManager) estimateTokens(bundle *CorrelationBundle) int {
	jsonStr := bundle.ModelDumpJSON()
	return len(jsonStr) / 4
}

//
// ===================== SSE SUPPORT =====================
//

func (s *StreamManager) Subscribe() chan *CorrelationBundle {
	ch := make(chan *CorrelationBundle, 1)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *StreamManager) Unsubscribe(ch chan *CorrelationBundle) {
	s.mu.Lock()
	delete(s.subscribers, ch)
	close(ch)
	s.mu.Unlock()
}

func (s *StreamManager) publish(bundle *CorrelationBundle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- bundle:
		default:
		}
	}
}

//
// ===================== PATTERN DERIVATION =====================
//

func deriveStreamPatterns(logs []RawLog) []string {
	unique := make(map[string]struct{})

	for _, rl := range logs {
		if msg, ok := rl.Data["message"].(string); ok && msg != "" {
			unique[msg] = struct{}{}
		}
	}

	var patterns []string
	for msg := range unique {
		patterns = append(patterns, msg)
	}
	return patterns
}
