package ledger

import (
	"context"
	"database/sql"
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
	if _, err := tx.Exec(`INSERT INTO plans (id, fingerprint, status, actor, checksum_algo, created_at, approved_at)
		VALUES (?,?,?,?,?,?,?)`, p.ID, p.Fingerprint, p.Status, p.Actor, p.ChecksumAlgo, p.CreatedAt, NullString(p.ApprovedAt)); err != nil {
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
			identity_snapshot_json, storage_class, publish_strategy, free_bytes, required_bytes, reserve_bytes)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, p.ID, NullString(d.DestinationID), d.Label, d.PeerID, d.RootPath, NullString(d.IdentityJSON),
			d.StorageClass, NullString(d.PublishStrategy), d.FreeBytes, d.RequiredBytes, d.ReserveBytes); err != nil {
			return err
		}
	}
	for i := range p.Entries {
		e := &p.Entries[i]
		res, err := tx.Exec(`INSERT INTO plan_entries (plan_id, plan_dest_id, source_root, source_rel, source_path,
			dest_rel_path, action, matched_rule, group_id, conflict_kind, exclusion_reason, excluded_by_user,
			camera_role, size, mtime_ns) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.ID, e.PlanDestID, e.SourceRoot, e.SourceRel, e.SourcePath, e.DestRelPath, e.Action,
			NullString(e.MatchedRule), NullString(e.GroupID), NullString(e.ConflictKind), NullString(e.ExclusionReason),
			e.ExcludedByUser, NullString(e.CameraRole), e.Size, e.MtimeNS)
		if err != nil {
			return err
		}
		if e.ID, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	return nil
}

// ApprovePlan approves a draft plan under exactly the fingerprint given.
func ApprovePlan(tx *sql.Tx, planID, fingerprint string) error {
	res, err := tx.Exec(`UPDATE plans SET status = ?, approved_at = ? WHERE id = ? AND fingerprint = ? AND status IN (?, ?)`,
		model.PlanApproved, Now(), planID, fingerprint, model.PlanDraft, model.PlanApproved)
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
	err := q.QueryRowContext(ctx, `SELECT fingerprint, status, actor, checksum_algo, created_at, approved_at FROM plans WHERE id = ?`, id).
		Scan(&p.Fingerprint, &p.Status, &p.Actor, &p.ChecksumAlgo, &p.CreatedAt, &approved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("plan %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	p.ApprovedAt = approved.String
	rows, err := q.QueryContext(ctx, `SELECT id, COALESCE(destination_id,''), label, peer_id, root_path,
		COALESCE(identity_snapshot_json,''), storage_class, COALESCE(publish_strategy,''), COALESCE(free_bytes,0),
		required_bytes, reserve_bytes FROM plan_destinations WHERE plan_id = ? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d model.PlanDestination
		if err := rows.Scan(&d.ID, &d.DestinationID, &d.Label, &d.PeerID, &d.RootPath, &d.IdentityJSON,
			&d.StorageClass, &d.PublishStrategy, &d.FreeBytes, &d.RequiredBytes, &d.ReserveBytes); err != nil {
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
	excluded_by_user, COALESCE(camera_role,''), size, mtime_ns FROM plan_entries`

type scanner interface{ Scan(dest ...any) error }

func scanEntry(s scanner) (model.PlanEntry, error) {
	var e model.PlanEntry
	err := s.Scan(&e.ID, &e.PlanDestID, &e.SourceRoot, &e.SourceRel, &e.SourcePath, &e.DestRelPath, &e.Action,
		&e.MatchedRule, &e.GroupID, &e.ConflictKind, &e.ExclusionReason, &e.ExcludedByUser, &e.CameraRole, &e.Size, &e.MtimeNS)
	return e, err
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	return errors.Join(err, rows.Close())
}
