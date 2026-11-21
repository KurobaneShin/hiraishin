package ledger

import (
	"encoding/json"
	"time"
)

type Kind string

const (
	KindProviderConfigured Kind = "provider.configured"
	KindQuotaSampled       Kind = "sensor.quota_sampled"
	KindProbeFailed        Kind = "sensor.probe_failed"
	KindLimitHit           Kind = "provider.limit_hit"
	KindAuthFailed         Kind = "provider.auth_failed"
	KindDegraded           Kind = "provider.degraded"
	KindRecovered          Kind = "provider.recovered"
	KindWindowReset        Kind = "provider.window_reset"
	KindPaused             Kind = "provider.paused"
	KindDraining           Kind = "provider.draining"
	KindResumed            Kind = "provider.resumed"
	KindProfileSet         Kind = "runtime.profile_set"
	KindPreferSet          Kind = "runtime.prefer_set"
	KindPreferCleared      Kind = "runtime.prefer_cleared"
	KindDecisionMade       Kind = "scheduler.decision_made"
	KindJobQueued          Kind = "job.queued"
	KindJobDispatched      Kind = "job.dispatched"
	KindJobSpawnFailed     Kind = "job.spawn_failed"
	KindSessionDetected    Kind = "job.session_detected"
	KindFailoverStarted    Kind = "job.failover_started"
	KindWaitingQuota       Kind = "job.waiting_quota"
	KindJobCompleted       Kind = "job.completed"
	KindJobFailed          Kind = "job.failed"
	KindJobCancelled       Kind = "job.cancelled"
	KindJobPinned          Kind = "job.pinned"
	KindJobUnpinned        Kind = "job.unpinned"
)

type Payload interface{ Kind() Kind }

type Event struct {
	ID         string
	TS         time.Time
	ProviderID string
	JobID      string
	Source     string
	Payload    Payload
}

const (
	SourceCLI       = "cli"
	SourceScheduler = "scheduler"
	SourceWatcher   = "watcher:regex"
	SourceSensor    = "sensor"
	SourceConfig    = "config"
)

type ProviderConfigured struct {
	Harness       string `json:"harness"`
	MaxConcurrent int    `json:"max_concurrent"`
}

type QuotaSampled struct {
	WindowName string    `json:"window_name"`
	WindowKind string    `json:"window_kind"`
	UsedPct    *float64  `json:"used_pct"`
	ResetsAt   time.Time `json:"resets_at,omitzero"`
	Confidence string    `json:"confidence"`
	RawTokens  *int64    `json:"raw_tokens,omitempty"`
}

type ProbeFailed struct {
	SensorTier string `json:"sensor_tier"`
	Error      string `json:"error"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type LimitHit struct {
	WindowName       string    `json:"window_name"`
	WindowKind       string    `json:"window_kind"`
	MatchedPattern   string    `json:"matched_pattern"`
	Excerpt          string    `json:"excerpt"`
	EstimatedResetAt time.Time `json:"estimated_reset_at,omitzero"`
}

type AuthFailed struct {
	MatchedPattern string `json:"matched_pattern"`
	Excerpt        string `json:"excerpt"`
	HTTPStatus     int    `json:"http_status,omitempty"`
}

type Degraded struct {
	MatchedPattern string    `json:"matched_pattern"`
	Excerpt        string    `json:"excerpt"`
	CooldownUntil  time.Time `json:"cooldown_until"`
}

type Recovered struct{}

type WindowReset struct {
	WindowName    string    `json:"window_name"`
	PreviousState string    `json:"previous_state"`
	ResetsAtNext  time.Time `json:"resets_at_next,omitzero"`
}

type Paused struct {
	Reason string `json:"reason,omitempty"`
	By     string `json:"by"`
}

type Draining struct {
	Reason string `json:"reason,omitempty"`
	By     string `json:"by"`
}

type Resumed struct {
	By string `json:"by"`
}

type ProfileSet struct {
	Profile string `json:"profile"`
	By      string `json:"by"`
}

type PreferSet struct {
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	By        string    `json:"by"`
}

type PreferCleared struct {
	Role string `json:"role"`
	By   string `json:"by"`
}

type DecisionMade struct {
	DecisionID       string          `json:"decision_id"`
	Trigger          string          `json:"trigger"`
	Profile          string          `json:"profile"`
	ChosenProvider   string          `json:"chosen_provider"`
	Role             string          `json:"role"`
	MinQualityTier   int             `json:"min_quality_tier"`
	Weights          json.RawMessage `json:"weights"`
	Candidates       json.RawMessage `json:"candidates"`
	Summary          string          `json:"summary"`
	EarliestResetAt  time.Time       `json:"earliest_reset_at,omitzero"`
	BlockedProviders []string        `json:"blocked_providers"`
}

type JobQueued struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	WorktreePath    string `json:"worktree_path"`
	Branch          string `json:"branch"`
	BriefPath       string `json:"brief_path"`
	ReportPath      string `json:"report_path"`
	TerminalAdapter string `json:"terminal_adapter"`
	PinnedProvider  string `json:"pinned_provider,omitempty"`
	NeverSwitch     bool   `json:"never_switch"`
}

type JobDispatched struct {
	AttemptID       string `json:"attempt_id"`
	AttemptNum      int    `json:"attempt_num"`
	DecisionID      string `json:"decision_id"`
	ProviderID      string `json:"provider_id"`
	TerminalAdapter string `json:"terminal_adapter"`
	TerminalHandle  string `json:"terminal_handle"`
	AttemptDir      string `json:"attempt_dir"`
	StartCommit     string `json:"start_commit"`
}

type JobSpawnFailed struct {
	DecisionID string `json:"decision_id"`
	Error      string `json:"error"`
	Retryable  bool   `json:"retryable"`
}

type SessionDetected struct {
	NativeSessionID string `json:"native_session_id"`
}

type FailoverStarted struct {
	AttemptID    string   `json:"attempt_id"`
	FromProvider string   `json:"from_provider"`
	Reason       string   `json:"reason"`
	ExitReason   string   `json:"exit_reason"`
	HeadCommit   string   `json:"head_commit"`
	DirtyFiles   []string `json:"dirty_files"`
}

type WaitingQuota struct {
	AttemptID        string    `json:"attempt_id,omitempty"`
	ExitReason       string    `json:"exit_reason,omitempty"`
	EndCommit        string    `json:"end_commit,omitempty"`
	EarliestResetAt  time.Time `json:"earliest_reset_at,omitzero"`
	BlockedProviders []string  `json:"blocked_providers"`
}

type JobCompleted struct {
	AttemptID   string  `json:"attempt_id"`
	EndCommit   string  `json:"end_commit"`
	DurationMin float64 `json:"duration_min"`
}

type JobFailed struct {
	AttemptID string `json:"attempt_id,omitempty"`
	EndCommit string `json:"end_commit,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Reason    string `json:"reason"`
}

type JobCancelled struct {
	AttemptID string `json:"attempt_id,omitempty"`
	By        string `json:"by"`
	Reason    string `json:"reason,omitempty"`
}

type JobPinned struct {
	Provider string `json:"provider"`
}

type JobUnpinned struct {
	NeverSwitch bool `json:"never_switch"`
}

func (ProviderConfigured) Kind() Kind { return KindProviderConfigured }

func (QuotaSampled) Kind() Kind { return KindQuotaSampled }

func (ProbeFailed) Kind() Kind { return KindProbeFailed }

func (LimitHit) Kind() Kind { return KindLimitHit }

func (AuthFailed) Kind() Kind { return KindAuthFailed }

func (Degraded) Kind() Kind { return KindDegraded }

func (Recovered) Kind() Kind { return KindRecovered }

func (WindowReset) Kind() Kind { return KindWindowReset }

func (Paused) Kind() Kind { return KindPaused }

func (Draining) Kind() Kind { return KindDraining }

func (Resumed) Kind() Kind { return KindResumed }

func (ProfileSet) Kind() Kind { return KindProfileSet }

func (PreferSet) Kind() Kind { return KindPreferSet }

func (PreferCleared) Kind() Kind { return KindPreferCleared }

func (DecisionMade) Kind() Kind { return KindDecisionMade }

func (JobQueued) Kind() Kind { return KindJobQueued }

func (JobDispatched) Kind() Kind { return KindJobDispatched }

func (JobSpawnFailed) Kind() Kind { return KindJobSpawnFailed }

func (SessionDetected) Kind() Kind { return KindSessionDetected }

func (FailoverStarted) Kind() Kind { return KindFailoverStarted }

func (WaitingQuota) Kind() Kind { return KindWaitingQuota }

func (JobCompleted) Kind() Kind { return KindJobCompleted }

func (JobFailed) Kind() Kind { return KindJobFailed }

func (JobCancelled) Kind() Kind { return KindJobCancelled }

func (JobPinned) Kind() Kind { return KindJobPinned }

func (JobUnpinned) Kind() Kind { return KindJobUnpinned }
