// Package model holds Ferry's canonical domain types. There is one front
// door for every workflow: sources and destinations are Locations, and a
// local drive-to-drive copy, a multi-destination fan-out and a
// cross-machine send are all the same Plan → Transfer → Receipt pipeline.
package model

// LocalPeer is the PeerID of this machine.
const LocalPeer = "local"

// Location is a path on a machine.
type Location struct {
	PeerID        string `json:"peer_id"`
	Path          string `json:"path"`
	DestinationID string `json:"destination_id,omitempty"`
}

// SourceSpec names one source folder or drive.
type SourceSpec struct {
	Location
	Label string `json:"label,omitempty"`
}

// DestinationSpec names one destination target: a saved destination
// (DestinationID) or a folder path. ConfirmBinding is the explicit,
// recorded confirmation a weak storage identity needs.
type DestinationSpec struct {
	Location
	Label          string `json:"label,omitempty"`
	ConfirmBinding bool   `json:"confirm_binding,omitempty"`
}

// UserExclusion is a reviewer's decision to leave an entry behind.
type UserExclusion struct {
	SourceLabel string `json:"source_label"`
	Rel         string `json:"rel"`
	Reason      string `json:"reason"`
}

// PlanRequest is the single planning entry point. CameraRoles maps a
// source label to a role override (e.g. "A-cam card" -> "ACAM").
type PlanRequest struct {
	Sources        []SourceSpec      `json:"sources"`
	Destinations   []DestinationSpec `json:"destinations"`
	PresetID       string            `json:"preset_id,omitempty"`
	PresetRev      int               `json:"preset_rev,omitempty"`
	ConflictPolicy string            `json:"conflict_policy,omitempty"`
	ChecksumAlgo   string            `json:"checksum_algo,omitempty"`
	CameraRoles    map[string]string `json:"camera_roles,omitempty"`
	Exclude        []UserExclusion   `json:"exclude,omitempty"`
	Actor          string            `json:"actor"`
}

// SavedDestination is a named, recognizable destination folder.
type SavedDestination struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	PeerID             string `json:"peer_id"`
	LocationKind       string `json:"location_kind"` // local_folder | volume_folder | mounted_share_folder
	RootPath           string `json:"root_path"`
	RelSubpath         string `json:"rel_subpath"`
	IdentityKind       string `json:"identity_kind"`
	IdentityValue      string `json:"identity_value"`
	IdentityConfidence string `json:"identity_confidence"`
	PinnedPresetID     string `json:"pinned_preset_id,omitempty"`
	PinnedPresetRev    int    `json:"pinned_preset_rev,omitempty"`
	DefaultConflict    string `json:"default_conflict"`
	ChecksumAlgo       string `json:"checksum_algo"`
	ReserveBytes       int64  `json:"reserve_bytes"`
	Archived           bool   `json:"archived"`
	CreatedAt          string `json:"created_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
}

// Plan statuses. An approval is single-use: starting a transfer moves the
// plan to executed.
const (
	PlanDraft       = "draft"
	PlanApproved    = "approved"
	PlanExecuted    = "executed"
	PlanInvalidated = "invalidated"
)

// Plan entry actions.
const (
	ActionCopy          = "copy"
	ActionDir           = "dir"
	ActionExclude       = "exclude"
	ActionSkipIdentical = "skip_identical"
	ActionNeedsReview   = "needs_review"
)

// Plan is an immutable, fingerprinted description of what will be written.
type Plan struct {
	ID           string            `json:"id"`
	Fingerprint  string            `json:"fingerprint"`
	Status       string            `json:"status"`
	Actor        string            `json:"actor"`
	ChecksumAlgo string            `json:"checksum_algo"`
	CreatedAt    string            `json:"created_at"`
	ApprovedAt   string            `json:"approved_at,omitempty"`
	Destinations []PlanDestination `json:"destinations"`
	Entries      []PlanEntry       `json:"entries"`
	Findings     []Finding         `json:"findings,omitempty"`
	RequestJSON  string            `json:"-"`
	DerivedFrom  string            `json:"derived_from,omitempty"`
	SupersededBy string            `json:"superseded_by,omitempty"`
	Invalidated  string            `json:"invalidated_reason,omitempty"`
}

// Finding severities.
const (
	SeverityBlocking = "blocking"
	SeverityWarning  = "warning"
)

// Finding is something review must see. Blocking findings refuse approval.
type Finding struct {
	Severity   string `json:"severity"`
	Kind       string `json:"kind"`
	PlanDestID string `json:"plan_dest_id,omitempty"`
	SourceRel  string `json:"source_rel,omitempty"`
	DestRel    string `json:"dest_rel,omitempty"`
	Message    string `json:"message"`
}

// Blocking reports whether any finding blocks approval.
func (p *Plan) Blocking() []Finding {
	var out []Finding
	for _, f := range p.Findings {
		if f.Severity == SeverityBlocking {
			out = append(out, f)
		}
	}
	return out
}

// PlanDestination is one target of a plan.
type PlanDestination struct {
	ID              string `json:"id"`
	DestinationID   string `json:"destination_id,omitempty"`
	Label           string `json:"label"`
	PeerID          string `json:"peer_id"`
	RootPath        string `json:"root_path"`
	IdentityJSON    string `json:"identity_snapshot,omitempty"`
	StorageClass    string `json:"storage_class"`
	PublishStrategy string `json:"publish_strategy,omitempty"`
	FreeBytes       int64  `json:"free_bytes"`
	RequiredBytes   int64  `json:"required_bytes"`
	ReserveBytes    int64  `json:"reserve_bytes"`
	PresetID        string `json:"preset_id,omitempty"`
	PresetRev       int    `json:"preset_rev,omitempty"`
	PresetHash      string `json:"preset_hash,omitempty"`
	ConflictPolicy  string `json:"conflict_policy,omitempty"`
}

// PlanEntry is one source item routed to one destination.
type PlanEntry struct {
	ID              int64  `json:"id"`
	PlanDestID      string `json:"plan_dest_id"`
	SourceRoot      string `json:"source_root"`
	SourceRel       string `json:"source_rel"`
	SourcePath      string `json:"source_path"`
	DestRelPath     string `json:"dest_rel_path"`
	Action          string `json:"action"`
	MatchedRule     string `json:"matched_rule,omitempty"`
	GroupID         string `json:"group_id,omitempty"`
	ConflictKind    string `json:"conflict_kind,omitempty"`
	ExclusionReason string `json:"exclusion_reason,omitempty"`
	ExcludedByUser  bool   `json:"excluded_by_user,omitempty"`
	CameraRole      string `json:"camera_role,omitempty"`
	Size            int64  `json:"size"`
	MtimeNS         int64  `json:"mtime_ns"`
	Kind            string `json:"kind"` // file | dir
	ExpectedHash    string `json:"expected_hash,omitempty"`
	RenamedFrom     string `json:"renamed_from,omitempty"`
	Warning         string `json:"warning,omitempty"`
	SourceLabel     string `json:"source_label,omitempty"`
	MetadataJSON    string `json:"metadata,omitempty"`
}

// Transfer statuses.
const (
	TransferQueued         = "queued"
	TransferRunning        = "running"
	TransferVerifying      = "verifying"
	TransferCompleted      = "completed"
	TransferPartial        = "partial"
	TransferNeedsAttention = "needs_attention"
	TransferFailed         = "failed"
	TransferCanceled       = "canceled"
)

// Item statuses. copying and publishing are the two durable in-flight
// states the crash-window reconciliation reads.
const (
	ItemPending          = "pending"
	ItemCopying          = "copying"
	ItemPublishing       = "publishing"
	ItemVerified         = "verified"
	ItemSkippedIdentical = "skipped_identical"
	ItemExcluded         = "excluded"
	ItemFailed           = "failed"
	ItemCanceled         = "canceled"
)

// ItemSettled reports a final, accounted-for item outcome.
func ItemSettled(status string) bool {
	return status == ItemVerified || status == ItemSkippedIdentical || status == ItemExcluded
}

// Transfer is one execution of an approved plan.
type Transfer struct {
	ID           string `json:"id"`
	PlanID       string `json:"plan_id"`
	Status       string `json:"status"`
	Actor        string `json:"actor"`
	ChecksumAlgo string `json:"checksum_algo"`
	StartedAt    string `json:"started_at,omitempty"`
	EndedAt      string `json:"ended_at,omitempty"`
	Canceled     bool   `json:"canceled"`
	ErrorSummary string `json:"error_summary,omitempty"`
}

// TransferTarget is one destination of a transfer.
type TransferTarget struct {
	ID              string  `json:"id"`
	TransferID      string  `json:"transfer_id"`
	PlanDestID      string  `json:"plan_dest_id"`
	Label           string  `json:"label"`
	PeerID          string  `json:"peer_id"`
	RootPath        string  `json:"root_path"`
	RootDev         uint64  `json:"-"`
	RootIno         uint64  `json:"-"`
	TransportKind   string  `json:"transport_kind"`
	PublishStrategy string  `json:"publish_strategy"`
	ReadbackMode    string  `json:"readback_mode,omitempty"`
	Status          string  `json:"status"`
	BytesWritten    int64   `json:"bytes_written"`
	BytesVerified   int64   `json:"bytes_verified"`
	FilesDone       int64   `json:"files_done"`
	FilesFailed     int64   `json:"files_failed"`
	EMAWriteBPS     float64 `json:"ema_write_bps"`
	EMAVerifyBPS    float64 `json:"ema_verify_bps"`
	Error           string  `json:"error,omitempty"`
	StartedAt       string  `json:"started_at,omitempty"`
	EndedAt         string  `json:"ended_at,omitempty"`
}

// TransferItem is one plan entry's execution on one target, joined with
// the plan entry it executes.
type TransferItem struct {
	ID              int64  `json:"id"`
	TargetID        string `json:"transfer_target_id"`
	PlanEntryID     int64  `json:"plan_entry_id"`
	Status          string `json:"status"`
	PartPath        string `json:"part_path,omitempty"`
	FinalPath       string `json:"final_path,omitempty"`
	ReservationIno  uint64 `json:"-"`
	BytesCopied     int64  `json:"bytes_copied"`
	SourceHash      string `json:"source_hash,omitempty"`
	DestHash        string `json:"dest_hash,omitempty"`
	ChecksumAlgo    string `json:"checksum_algo,omitempty"`
	ReadbackMode    string `json:"readback_mode,omitempty"`
	PublishStrategy string `json:"publish_strategy,omitempty"`
	MtimePreserved  *bool  `json:"mtime_preserved,omitempty"`
	Error           string `json:"error,omitempty"`
	Warning         string `json:"warning,omitempty"`
	Attempts        int    `json:"attempts"`

	Entry PlanEntry `json:"entry"`
}
