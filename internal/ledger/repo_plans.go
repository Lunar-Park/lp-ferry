package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Lunar-Park/lp-ferry/internal/model"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrStaleApproval is returned when a plan is not approved under the
// fingerprint the caller presents.
var ErrStaleApproval = errors.New("plan is not approved under that fingerprint")

// InsertPlan writes a plan, its destinations and entries. Entry IDs are
// assigned and written back into p.
func InsertPlan(tx *sql.Tx, p *model.Plan) error {
	if p.CreatedAt == "" {
		p.CreatedAt = Now()
	}
	if p.Status == "" {
		p.Status = model.PlanDraft
	}
	findings, err := json.Marshal(p.Findings)
	if err != nil {
		return err
	}
	if p.Findings == nil {
		findings = []byte("[]")
	}
	if _, err := tx.Exec(`INSERT INTO plans (id, fingerprint, status, actor, checksum_algo, created_at, approved_at,
		request_json, findings_json, derived_from, preset_id, preset_rev, preset_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.Fingerprint, p.Status, p.Actor, p.ChecksumAlgo, p.CreatedAt,
		NullString(p.ApprovedAt), NullString(p.RequestJSON), string(findings), NullString(p.DerivedFrom),
		NullString(firstPresetID(p)), firstPresetRev(p), NullString(firstPresetHash(p))); err != nil {
		return err
	}
	for _, d := range p.Destinations {
		if d.PeerID == "" {
			d.PeerID = model.LocalPeer
		}
		if d.StorageClass == "" {
			d.StorageClass = "unknown"
		}
		if _, err := tx.Exec(`INSERT INTO plan_destinations (id, plan_id, destination_id, label, peer_id, root_path,
			identity_snapshot_json, storage_class, publish_strategy, free_bytes, required_bytes, reserve_bytes,
			preset_id, preset_rev, preset_hash, conflict_policy)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, p.ID, NullString(d.DestinationID), d.Label, d.PeerID, d.RootPath, NullString(d.IdentityJSON),
			d.StorageClass, NullString(d.PublishStrategy), d.FreeBytes, d.RequiredBytes, d.ReserveBytes,
			NullString(d.PresetID), nullInt(d.PresetRev), NullString(d.PresetHash), NullString(d.ConflictPolicy)); err != nil {
			return err
		}
	}
	stmt, err := tx.Prepare(`INSERT INTO plan_entries (plan_id, plan_dest_id, source_root, source_rel, source_path,
		dest_rel_path, action, matched_rule, group_id, conflict_kind, exclusion_reason, excluded_by_user,
		camera_role, size, mtime_ns, entry_kind, expected_hash, renamed_from, warning, source_label, metadata_json, source_peer)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for i := range p.Entries {
		e := &p.Entries[i]
		if e.Kind == "" {
			e.Kind = "file"
		}
		if e.SourcePeer == "" {
			e.SourcePeer = model.LocalPeer
		}
		res, err := stmt.Exec(p.ID, e.PlanDestID, e.SourceRoot, e.SourceRel, e.SourcePath, e.DestRelPath, e.Action,
			NullString(e.MatchedRule), NullString(e.GroupID), NullString(e.ConflictKind), NullString(e.ExclusionReason),
			e.ExcludedByUser, NullString(e.CameraRole), e.Size, e.MtimeNS, e.Kind, NullString(e.ExpectedHash),
			NullString(e.RenamedFrom), NullString(e.Warning), NullString(e.SourceLabel), NullString(e.MetadataJSON), e.SourcePeer)
		if err != nil {
			return err
		}
		if e.ID, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	return nil
}

func firstPresetID(p *model.Plan) string {
	if len(p.Destinations) > 0 {
		return p.Destinations[0].PresetID
	}
	return ""
}

func firstPresetRev(p *model.Plan) sql.NullInt64 {
	if len(p.Destinations) > 0 {
		return nullInt(p.Destinations[0].PresetRev)
	}
	return sql.NullInt64{}
}

func firstPresetHash(p *model.Plan) string {
	if len(p.Destinations) > 0 {
		return p.Destinations[0].PresetHash
	}
	return ""
}

func nullInt(v int) sql.NullInt64 { return sql.NullInt64{Int64: int64(v), Valid: v != 0} }

// RecordCameraKeys notes which camera bindings a plan's routing used.
func RecordCameraKeys(tx *sql.Tx, planID string, keys []string) error {
	for _, k := range keys {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO plan_camera_keys (plan_id, camera_key) VALUES (?,?)`, planID, k); err != nil {
			return err
		}
	}
	return nil
}

// pendingPlans is the set of statuses an edit can still invalidate.
const pendingPlans = `('draft','awaiting_approval','approved')`

// InvalidatePlan marks one pending plan invalidated with a reason.
func InvalidatePlan(tx *sql.Tx, planID, reason, supersededBy string) error {
	_, err := tx.Exec(`UPDATE plans SET status='invalidated', invalidated_reason=?, superseded_by=COALESCE(?, superseded_by)
		WHERE id=? AND status IN `+pendingPlans, reason, NullString(supersededBy), planID)
	return err
}

// InvalidatePlansForPreset invalidates every pending plan built on a preset.
func InvalidatePlansForPreset(tx *sql.Tx, presetID, reason string) (int64, error) {
	return invalidateWhere(tx, reason, `id IN (SELECT plan_id FROM plan_destinations WHERE preset_id = ?)`, presetID)
}

// InvalidatePlansForDestination invalidates every pending plan writing to a saved destination.
func InvalidatePlansForDestination(tx *sql.Tx, destinationID, reason string) (int64, error) {
	return invalidateWhere(tx, reason, `id IN (SELECT plan_id FROM plan_destinations WHERE destination_id = ?)`, destinationID)
}

// InvalidatePlansForCamera invalidates every pending plan routed by a camera binding.
func InvalidatePlansForCamera(tx *sql.Tx, cameraKey, reason string) (int64, error) {
	return invalidateWhere(tx, reason, `id IN (SELECT plan_id FROM plan_camera_keys WHERE camera_key = ?)`, cameraKey)
}

func invalidateWhere(tx *sql.Tx, reason, where string, arg any) (int64, error) {
	//nolint:gosec // where is one of the fixed clauses above; values are bound
	res, err := tx.Exec(`UPDATE plans SET status='invalidated', invalidated_reason=? WHERE status IN `+pendingPlans+` AND `+where, reason, arg)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkPlanExecuted consumes an approval when a transfer starts from it.
func MarkPlanExecuted(tx *sql.Tx, planID, fingerprint string) error {
	res, err := tx.Exec(`UPDATE plans SET status='executed' WHERE id=? AND fingerprint=? AND status='approved'`, planID, fingerprint)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrStaleApproval, planID)
	}
	return nil
}

// ListPlanEntries pages a plan's entries in stable id order. destID ""
// means every destination. Returns the page and the total.
func ListPlanEntries(ctx context.Context, q querier, planID, destID string, afterID int64, limit int) ([]model.PlanEntry, int64, error) {
	var total int64
	args := []any{planID}
	where := ` WHERE plan_id = ?`
	if destID != "" {
		where += ` AND plan_dest_id = ?`
		args = append(args, destID)
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM plan_entries`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx, entrySelect+where+` AND id > ? ORDER BY id LIMIT ?`, append(args, afterID, limit)...)
	if err != nil {
		return nil, 0, err
	}
	var out []model.PlanEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, closeRows(rows)
}

// ApprovePlan approves a draft plan under exactly the fingerprint given.
func ApprovePlan(tx *sql.Tx, planID, fingerprint string) error {
	res, err := tx.Exec(`UPDATE plans SET status = ?, approved_at = ? WHERE id = ? AND fingerprint = ? AND status IN (?, ?, ?)`,
		model.PlanApproved, Now(), planID, fingerprint, model.PlanDraft, model.PlanAwaiting, model.PlanApproved)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrStaleApproval, planID)
	}
	return nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// GetPlan loads a plan with destinations and entries.
func GetPlan(ctx context.Context, q querier, id string) (*model.Plan, error) {
	p := &model.Plan{ID: id}
	var approved sql.NullString
	var findings string
	var req, derived, superseded, invalid sql.NullString
	err := q.QueryRowContext(ctx, `SELECT fingerprint, status, actor, checksum_algo, created_at, approved_at,
		request_json, findings_json, derived_from, superseded_by, invalidated_reason FROM plans WHERE id = ?`, id).
		Scan(&p.Fingerprint, &p.Status, &p.Actor, &p.ChecksumAlgo, &p.CreatedAt, &approved, &req, &findings, &derived, &superseded, &invalid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("plan %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	p.ApprovedAt, p.RequestJSON, p.DerivedFrom, p.SupersededBy, p.Invalidated = approved.String, req.String, derived.String, superseded.String, invalid.String
	if err := json.Unmarshal([]byte(findings), &p.Findings); err != nil {
		return nil, fmt.Errorf("plan %s findings: %w", id, err)
	}
	rows, err := q.QueryContext(ctx, `SELECT id, COALESCE(destination_id,''), label, peer_id, root_path,
		COALESCE(identity_snapshot_json,''), storage_class, COALESCE(publish_strategy,''), COALESCE(free_bytes,0),
		required_bytes, reserve_bytes, COALESCE(preset_id,''), COALESCE(preset_rev,0), COALESCE(preset_hash,''),
		COALESCE(conflict_policy,'') FROM plan_destinations WHERE plan_id = ? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d model.PlanDestination
		if err := rows.Scan(&d.ID, &d.DestinationID, &d.Label, &d.PeerID, &d.RootPath, &d.IdentityJSON,
			&d.StorageClass, &d.PublishStrategy, &d.FreeBytes, &d.RequiredBytes, &d.ReserveBytes,
			&d.PresetID, &d.PresetRev, &d.PresetHash, &d.ConflictPolicy); err != nil {
			_ = rows.Close()
			return nil, err
		}
		p.Destinations = append(p.Destinations, d)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, entrySelect+` WHERE plan_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		p.Entries = append(p.Entries, e)
	}
	return p, closeRows(rows)
}

const entrySelect = `SELECT id, plan_dest_id, source_root, source_rel, source_path, dest_rel_path, action,
	COALESCE(matched_rule,''), COALESCE(group_id,''), COALESCE(conflict_kind,''), COALESCE(exclusion_reason,''),
	excluded_by_user, COALESCE(camera_role,''), size, mtime_ns, entry_kind, COALESCE(expected_hash,''),
	COALESCE(renamed_from,''), COALESCE(warning,''), COALESCE(source_label,''), COALESCE(metadata_json,''), source_peer FROM plan_entries`

type scanner interface{ Scan(dest ...any) error }

func scanEntry(s scanner) (model.PlanEntry, error) {
	var e model.PlanEntry
	err := s.Scan(&e.ID, &e.PlanDestID, &e.SourceRoot, &e.SourceRel, &e.SourcePath, &e.DestRelPath, &e.Action,
		&e.MatchedRule, &e.GroupID, &e.ConflictKind, &e.ExclusionReason, &e.ExcludedByUser, &e.CameraRole, &e.Size, &e.MtimeNS,
		&e.Kind, &e.ExpectedHash, &e.RenamedFrom, &e.Warning, &e.SourceLabel, &e.MetadataJSON, &e.SourcePeer)
	return e, err
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	return errors.Join(err, rows.Close())
}

// MarkPlanAwaiting records that an agent requested this plan and policy
// requires a human to approve it.
func MarkPlanAwaiting(tx *sql.Tx, planID, fingerprint string) error {
	res, err := tx.Exec(`UPDATE plans SET status='awaiting_approval' WHERE id=? AND fingerprint=? AND status='draft'`, planID, fingerprint)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrStaleApproval, planID)
	}
	return nil
}
