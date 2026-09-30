// Package checkercfg holds the shared config/session/progress types used by
// the checker engine, the xbox validation flow and the GUI.
package checkercfg

import "sync"

type LineRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type Session struct {
	ComboPath     string              `json:"combo_path"`
	ProxyInput    string              `json:"proxy_input"`
	KeywordsInput string              `json:"keywords_input"`
	Workers       int                 `json:"workers"`
	DoneRanges    []LineRange         `json:"done_ranges"`
	RetryLines    []int               `json:"retry_lines"`
	RetryCounts   map[string]int      `json:"retry_counts"`
	TriedProxies  map[string][]string `json:"tried_proxies"`
	UpdatedAt     string              `json:"updated_at"`
	StartedAt     string              `json:"started_at"`
	File          string              `json:"file,omitempty"`
	CapKeywords   bool                `json:"cap_keywords"`
	CapPayments   bool                `json:"cap_payments"`
	CapPromos     bool                `json:"cap_promos"`
	CapFortnite   bool                `json:"cap_fortnite"`
	CapturesSet   bool                `json:"captures_set"`
}

type Config struct {
	ComboPath     string
	ProxyInput    string
	KeywordsInput string
	Proxies       []string // empty = no proxy; supports single proxy or list loaded from .txt
	Workers       int
	Keywords      []string // keywords to search in each inbox
	CapKeywords   bool
	CapPayments   bool
	CapPromos     bool
	CapFortnite   bool
	Session       *Session
	SessMu        *sync.Mutex
}

type ProgressEvent struct {
	IsValid    bool
	Hit        string
	LogMsg     string
	IsDone     bool
	IsRetry    bool // true when an account was queued for retry
	RetryDelta int
	Daily      int
	LimitHit   bool
}
