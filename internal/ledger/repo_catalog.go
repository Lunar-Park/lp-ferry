package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Lunar-Park/lp-ferry/internal/model"
)

const destSelect = `SELECT id, name, peer_id, location_kind, root_path, rel_subpath, identity_kind, identity_value,
	identity_confidence, COALESCE(pinned_preset_id,''), COALESCE(pinned_preset_rev,0), default_conflict, checksum_algo,
	reserve_bytes, archived, created_at, updated_at FROM saved_destinations`

func scanDest(s scanner) (model.SavedDestination, error) {
	var d model.SavedDestination
	err := s.Scan(&d.ID, &d.Name, &d.PeerID, &d.LocationKind, &d.RootPath, &d.RelSubpath, &d.IdentityKind, &d.IdentityValue,
		&d.IdentityConfidence, &d.PinnedPresetID, &d.PinnedPresetRev, &d.DefaultConflict, &d.ChecksumAlgo, &d.ReserveBytes,
		&d.Archived, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

// GetDestination loads a saved destination.
func GetDestination(ctx context.Context, q querier, id string) (*model.SavedDestination, error) {
	d, err := scanDest(q.QueryRowContext(ctx, destSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("destination %s: %w", id, ErrNotFound)
	}
	return &d, err
}

// ListDestinations lists saved destinations (archived ones only if asked).
func ListDestinations(ctx context.Context, q querier, includeArchived bool) ([]model.SavedDestination, error) {
	where := ` WHERE archived = 0`
	if includeArchived {
		where = ``
	}
	rows, err := q.QueryContext(ctx, destSelect+where+` ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	var out []model.SavedDestination
	for rows.Next() {
		d, err := scanDest(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	return out, closeRows(rows)
}

// SaveDestination inserts or updates a saved destination. Updating one
// invalidates every pending plan that writes to it, in the same
// transaction: configuration changed, so the reviewed plan no longer
// describes what would happen.
func SaveDestination(tx *sql.Tx, d *model.SavedDestination) (bool, error) {
	now := Now()
	d.UpdatedAt = now
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM saved_destinations WHERE id = ?`, d.ID).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		d.CreatedAt = now
		_, err := tx.Exec(`INSERT INTO saved_destinations (id, name, peer_id, location_kind, root_path, rel_subpath,
			identity_kind, identity_value, identity_confidence, pinned_preset_id, pinned_preset_rev, default_conflict,
			checksum_algo, reserve_bytes, archived, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, d.Name, d.PeerID, d.LocationKind, d.RootPath, d.RelSubpath, d.IdentityKind, d.IdentityValue,
			d.IdentityConfidence, NullString(d.PinnedPresetID), nullInt(d.PinnedPresetRev), d.DefaultConflict, d.ChecksumAlgo,
			d.ReserveBytes, d.Archived, now, now)
		return true, err
	}
	if _, err := tx.Exec(`UPDATE saved_destinations SET name=?, peer_id=?, location_kind=?, root_path=?, rel_subpath=?,
		identity_kind=?, identity_value=?, identity_confidence=?, pinned_preset_id=?, pinned_preset_rev=?, default_conflict=?,
		checksum_algo=?, reserve_bytes=?, archived=?, updated_at=? WHERE id=?`,
		d.Name, d.PeerID, d.LocationKind, d.RootPath, d.RelSubpath, d.IdentityKind, d.IdentityValue, d.IdentityConfidence,
		NullString(d.PinnedPresetID), nullInt(d.PinnedPresetRev), d.DefaultConflict, d.ChecksumAlgo, d.ReserveBytes,
		d.Archived, now, d.ID); err != nil {
		return false, err
	}
	_, err := InvalidatePlansForDestination(tx, d.ID, "destination "+d.ID+" was edited")
	return false, err
}

// CameraBinding maps one physical camera to its default role.
type CameraBinding struct {
	Make        string `json:"make"`
	Model       string `json:"model"`
	Serial      string `json:"serial"`
	DefaultRole string `json:"default_role"`
	UpdatedAt   string `json:"updated_at"`
}

// BindCamera records (or changes) a camera's default role and invalidates
// pending plans whose routing used that camera's previous role.
func BindCamera(tx *sql.Tx, b CameraBinding, key string) error {
	b.UpdatedAt = Now()
	if _, err := tx.Exec(`INSERT INTO camera_bindings (make, model, serial, default_role, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(make, model, serial) DO UPDATE SET default_role=excluded.default_role, updated_at=excluded.updated_at`,
		b.Make, b.Model, b.Serial, b.DefaultRole, b.UpdatedAt); err != nil {
		return err
	}
	_, err := InvalidatePlansForCamera(tx, key, "camera binding "+key+" changed to "+b.DefaultRole)
	return err
}

// CameraRoles returns every binding keyed by the caller's key function.
func CameraRoles(ctx context.Context, q querier, key func(make, model, serial string) string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT make, model, serial, default_role FROM camera_bindings`)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for rows.Next() {
		var mk, md, sn, role string
		if err := rows.Scan(&mk, &md, &sn, &role); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[key(mk, md, sn)] = role
	}
	return out, closeRows(rows)
}

// InventoryRow is one scanned entry to persist.
type InventoryRow struct {
	Rel, Type, Status, Error, MetadataJSON string
	Size, MtimeNS                          int64
}

// InsertInventory persists a full source inventory (never capped).
func InsertInventory(tx *sql.Tx, id, peerID, path, label, manifest, cardJSON string, files, bytes, errs int64, rows []InventoryRow) error {
	now := Now()
	if _, err := tx.Exec(`INSERT INTO inventories (id, peer_id, source_path, source_label, status, file_count, total_bytes,
		error_count, camera_hint_json, manifest_hash, created_at, completed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, peerID, path, label, "complete", files, bytes, errs, NullString(cardJSON), manifest, now, now); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO inventory_entries (inventory_id, rel_path, entry_type, size, mtime_ns, scan_status,
		scan_error, metadata_json) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, r := range rows {
		if _, err := stmt.Exec(id, r.Rel, r.Type, r.Size, r.MtimeNS, r.Status, NullString(r.Error), NullString(r.MetadataJSON)); err != nil {
			return err
		}
	}
	return nil
}
