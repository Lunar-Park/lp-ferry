package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/Lunar-Park/lp-ferry/internal/model"
)

// InsertTransfer writes a transfer, its targets and one pending item per
// (target, plan entry).
func InsertTransfer(tx *sql.Tx, t *model.Transfer, targets []model.TransferTarget, items []model.TransferItem) error {
	if _, err := tx.Exec(`INSERT INTO transfers (id, plan_id, status, actor, checksum_algo, started_at) VALUES (?,?,?,?,?,?)`,
		t.ID, t.PlanID, t.Status, t.Actor, t.ChecksumAlgo, NullString(t.StartedAt)); err != nil {
		return err
	}
	for _, g := range targets {
		if _, err := tx.Exec(`INSERT INTO transfer_targets (id, transfer_id, plan_dest_id, label, peer_id, root_path,
			root_dev, root_ino, transport_kind, publish_strategy, readback_mode, status, started_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, g.ID, t.ID, g.PlanDestID, g.Label, g.PeerID, g.RootPath,
			int64(g.RootDev), int64(g.RootIno), g.TransportKind, g.PublishStrategy, NullString(g.ReadbackMode), g.Status,
			NullString(g.StartedAt)); err != nil {
			return err
		}
	}
	now := Now()
	for _, it := range items {
		if _, err := tx.Exec(`INSERT INTO transfer_items (transfer_target_id, plan_entry_id, status, final_path,
			checksum_algo, publish_strategy, error, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			it.TargetID, it.PlanEntryID, it.Status, NullString(it.FinalPath), NullString(it.ChecksumAlgo),
			NullString(it.PublishStrategy), NullString(it.Error), now); err != nil {
			return err
		}
	}
	return nil
}

// GetTransfer loads one transfer row.
func GetTransfer(ctx context.Context, q querier, id string) (*model.Transfer, error) {
	t := &model.Transfer{ID: id}
	var started, ended, summary sql.NullString
	err := q.QueryRowContext(ctx, `SELECT plan_id, status, actor, checksum_algo, started_at, ended_at, canceled, error_summary
		FROM transfers WHERE id = ?`, id).Scan(&t.PlanID, &t.Status, &t.Actor, &t.ChecksumAlgo, &started, &ended, &t.Canceled, &summary)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("transfer %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	t.StartedAt, t.EndedAt, t.ErrorSummary = started.String, ended.String, summary.String
	return t, nil
}

// SetTransferStatus updates status (and ended_at for terminal states).
func SetTransferStatus(tx *sql.Tx, id, status, summary string, terminal bool) error {
	ended := sql.NullString{}
	if terminal {
		ended = NullString(Now())
	}
	_, err := tx.Exec(`UPDATE transfers SET status = ?, error_summary = ?, ended_at = COALESCE(?, ended_at),
		canceled = CASE WHEN ? = 'canceled' THEN 1 ELSE canceled END WHERE id = ?`,
		status, NullString(summary), ended, status, id)
	return err
}

// ListTransfersByStatus returns transfer IDs in any of the given statuses.
func ListTransfersByStatus(ctx context.Context, q querier, statuses ...string) ([]string, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	args := make([]any, len(statuses))
	for i, s := range statuses {
		args[i] = s
	}
	//nolint:gosec // only "?" placeholders are concatenated; values are bound
	rows, err := q.QueryContext(ctx, `SELECT id FROM transfers WHERE status IN (?`+strings.Repeat(",?", len(statuses)-1)+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, closeRows(rows)
}

// GetTargets loads a transfer's targets.
func GetTargets(ctx context.Context, q querier, transferID string) ([]model.TransferTarget, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, plan_dest_id, label, peer_id, root_path, COALESCE(root_dev,0), COALESCE(root_ino,0),
		transport_kind, COALESCE(publish_strategy,''), COALESCE(readback_mode,''), status, bytes_written, bytes_verified,
		files_done, files_failed, ema_write_bps, ema_verify_bps, COALESCE(error,''), COALESCE(started_at,''), COALESCE(ended_at,'')
		FROM transfer_targets WHERE transfer_id = ? ORDER BY rowid`, transferID)
	if err != nil {
		return nil, err
	}
	var out []model.TransferTarget
	for rows.Next() {
		g := model.TransferTarget{TransferID: transferID}
		var dev, ino int64
		if err := rows.Scan(&g.ID, &g.PlanDestID, &g.Label, &g.PeerID, &g.RootPath, &dev, &ino, &g.TransportKind,
			&g.PublishStrategy, &g.ReadbackMode, &g.Status, &g.BytesWritten, &g.BytesVerified, &g.FilesDone, &g.FilesFailed,
			&g.EMAWriteBPS, &g.EMAVerifyBPS, &g.Error, &g.StartedAt, &g.EndedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		g.RootDev, g.RootIno = uint64(dev), uint64(ino)
		out = append(out, g)
	}
	return out, closeRows(rows)
}

// UpdateTarget writes a target's mutable fields.
func UpdateTarget(tx *sql.Tx, g *model.TransferTarget) error {
	_, err := tx.Exec(`UPDATE transfer_targets SET status=?, root_dev=?, root_ino=?, publish_strategy=?, readback_mode=?,
		bytes_written=?, bytes_verified=?, files_done=?, files_failed=?, ema_write_bps=?, ema_verify_bps=?, error=?,
		started_at=COALESCE(started_at, ?), ended_at=? WHERE id=?`,
		g.Status, int64(g.RootDev), int64(g.RootIno), NullString(g.PublishStrategy), NullString(g.ReadbackMode),
		g.BytesWritten, g.BytesVerified, g.FilesDone, g.FilesFailed, g.EMAWriteBPS, g.EMAVerifyBPS, NullString(g.Error),
		NullString(g.StartedAt), NullString(g.EndedAt), g.ID)
	return err
}

const itemSelect = `SELECT i.id, i.transfer_target_id, i.plan_entry_id, i.status, COALESCE(i.part_path,''), COALESCE(i.final_path,''),
	COALESCE(i.reservation_ino,0), i.bytes_copied, COALESCE(i.source_hash,''), COALESCE(i.dest_hash,''), COALESCE(i.checksum_algo,''),
	COALESCE(i.readback_mode,''), COALESCE(i.publish_strategy,''), i.mtime_preserved, COALESCE(i.error,''), COALESCE(i.warning,''), i.attempts,
	e.id, e.plan_dest_id, e.source_root, e.source_rel, e.source_path, e.dest_rel_path, e.action,
	COALESCE(e.matched_rule,''), COALESCE(e.group_id,''), COALESCE(e.conflict_kind,''), COALESCE(e.exclusion_reason,''),
	e.excluded_by_user, COALESCE(e.camera_role,''), e.size, e.mtime_ns, e.entry_kind, COALESCE(e.expected_hash,''),
	COALESCE(e.renamed_from,''), COALESCE(e.warning,''), COALESCE(e.source_label,''), COALESCE(e.metadata_json,''), e.source_peer
	FROM transfer_items i JOIN plan_entries e ON e.id = i.plan_entry_id
	JOIN transfer_targets t ON t.id = i.transfer_target_id`

// GetItems loads every item of a transfer joined with its plan entry,
// ordered by plan entry then target.
func GetItems(ctx context.Context, q querier, transferID string) ([]model.TransferItem, error) {
	rows, err := q.QueryContext(ctx, itemSelect+` WHERE t.transfer_id = ? ORDER BY i.plan_entry_id, i.transfer_target_id`, transferID)
	if err != nil {
		return nil, err
	}
	var out []model.TransferItem
	for rows.Next() {
		var it model.TransferItem
		var ino int64
		var mp sql.NullBool
		e := &it.Entry
		if err := rows.Scan(&it.ID, &it.TargetID, &it.PlanEntryID, &it.Status, &it.PartPath, &it.FinalPath, &ino,
			&it.BytesCopied, &it.SourceHash, &it.DestHash, &it.ChecksumAlgo, &it.ReadbackMode, &it.PublishStrategy, &mp,
			&it.Error, &it.Warning, &it.Attempts,
			&e.ID, &e.PlanDestID, &e.SourceRoot, &e.SourceRel, &e.SourcePath, &e.DestRelPath, &e.Action,
			&e.MatchedRule, &e.GroupID, &e.ConflictKind, &e.ExclusionReason, &e.ExcludedByUser, &e.CameraRole, &e.Size, &e.MtimeNS,
			&e.Kind, &e.ExpectedHash, &e.RenamedFrom, &e.Warning, &e.SourceLabel, &e.MetadataJSON, &e.SourcePeer); err != nil {
			_ = rows.Close()
			return nil, err
		}
		it.ReservationIno = uint64(ino)
		if mp.Valid {
			v := mp.Bool
			it.MtimePreserved = &v
		}
		out = append(out, it)
	}
	return out, closeRows(rows)
}

// UpdateItem writes an item's mutable fields.
func UpdateItem(tx *sql.Tx, it *model.TransferItem) error {
	var mp sql.NullBool
	if it.MtimePreserved != nil {
		mp = sql.NullBool{Bool: *it.MtimePreserved, Valid: true}
	}
	_, err := tx.Exec(`UPDATE transfer_items SET status=?, part_path=?, final_path=?, reservation_ino=?, bytes_copied=?,
		source_hash=?, dest_hash=?, checksum_algo=?, readback_mode=?, publish_strategy=?, mtime_preserved=?, error=?, warning=?,
		attempts=?, updated_at=? WHERE id=?`,
		it.Status, NullString(it.PartPath), NullString(it.FinalPath), int64(it.ReservationIno), it.BytesCopied,
		NullString(it.SourceHash), NullString(it.DestHash), NullString(it.ChecksumAlgo), NullString(it.ReadbackMode),
		NullString(it.PublishStrategy), mp, NullString(it.Error), NullString(it.Warning), it.Attempts, Now(), it.ID)
	return err
}

// ReservationKey normalizes a destination-relative path for the
// cross-job reservation table. It folds case and Unicode normalization so
// "Clip.MOV" and "clip.mov", or NFC and NFD "é", collide here exactly as
// they would on a case-insensitive or normalizing filesystem. Over-
// reserving is safe; under-reserving lets two jobs race for one name.
func ReservationKey(rel string) string {
	return strings.ToLower(norm.NFC.String(rel))
}

// ReservationConflict names the job already holding a path.
type ReservationConflict struct {
	DestKey    string
	Path       string
	HolderID   string
	HolderStat string
}

func (c *ReservationConflict) Error() string {
	return fmt.Sprintf("%s is reserved by transfer %s (%s)", c.Path, c.HolderID, c.HolderStat)
}

// ReservePaths reserves every (destKey, rel) for transferID or fails with
// the first *ReservationConflict, reserving nothing.
func ReservePaths(tx *sql.Tx, transferID string, keys [][2]string) error {
	now := Now()
	for _, k := range keys {
		var holder, status string
		err := tx.QueryRow(`SELECT r.transfer_id, t.status FROM path_reservations r JOIN transfers t ON t.id = r.transfer_id
			WHERE r.dest_key = ? AND r.dest_rel_path = ?`, k[0], ReservationKey(k[1])).Scan(&holder, &status)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case holder != transferID:
			return &ReservationConflict{DestKey: k[0], Path: k[1], HolderID: holder, HolderStat: status}
		default:
			continue
		}
		if _, err := tx.Exec(`INSERT INTO path_reservations (dest_key, dest_rel_path, transfer_id, created_at) VALUES (?,?,?,?)`,
			k[0], ReservationKey(k[1]), transferID, now); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseReservations drops every reservation a transfer holds.
func ReleaseReservations(tx *sql.Tx, transferID string) error {
	_, err := tx.Exec(`DELETE FROM path_reservations WHERE transfer_id = ?`, transferID)
	return err
}

// ReleaseDeadReservations drops reservations held by transfers in a
// terminal state (a crash can leave them behind).
func ReleaseDeadReservations(tx *sql.Tx, alive ...string) (int64, error) {
	args := make([]any, len(alive))
	for i, s := range alive {
		args[i] = s
	}
	//nolint:gosec // only "?" placeholders are concatenated; values are bound
	res, err := tx.Exec(`DELETE FROM path_reservations WHERE transfer_id NOT IN
		(SELECT id FROM transfers WHERE status IN (?`+strings.Repeat(",?", len(alive)-1)+`))`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Receipt row.
type Receipt struct {
	ID           string
	TransferID   string
	PlanID       string
	Status       string
	Actor        string
	Digest       string
	BodyJSON     string
	ExportedPath string
	ExportError  string
	CreatedAt    string
}

// InsertReceipt writes a receipt row.
func InsertReceipt(tx *sql.Tx, r *Receipt) error {
	_, err := tx.Exec(`INSERT INTO receipts (id, transfer_id, plan_id, status, actor, digest, body_json, created_at)
		VALUES (?,?,?,?,?,?,?,?)`, r.ID, r.TransferID, r.PlanID, r.Status, r.Actor, r.Digest, r.BodyJSON, r.CreatedAt)
	return err
}

// SetReceiptExport records the export outcome.
func SetReceiptExport(tx *sql.Tx, id, path, exportErr string) error {
	_, err := tx.Exec(`UPDATE receipts SET exported_path = ?, export_error = ? WHERE id = ?`, NullString(path), NullString(exportErr), id)
	return err
}

// LatestReceipt returns the newest receipt for a transfer.
func LatestReceipt(ctx context.Context, q querier, transferID string) (*Receipt, error) {
	r := &Receipt{TransferID: transferID}
	var exported, exportErr sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id, plan_id, status, actor, digest, body_json, exported_path, export_error, created_at
		FROM receipts WHERE transfer_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, transferID).
		Scan(&r.ID, &r.PlanID, &r.Status, &r.Actor, &r.Digest, &r.BodyJSON, &exported, &exportErr, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("receipt for %s: %w", transferID, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	r.ExportedPath, r.ExportError = exported.String, exportErr.String
	return r, nil
}

// Verification row.
type Verification struct {
	ReceiptID, TransferID, PeerID, Folder, ReadbackMode, DetailJSON string
	Checked, Missing, Modified, Added, Unreadable                   int
	Clean                                                           bool
}

// InsertVerification records one verify run.
func InsertVerification(tx *sql.Tx, v *Verification) error {
	_, err := tx.Exec(`INSERT INTO verifications (receipt_id, transfer_id, peer_id, folder, checked, missing, modified, added,
		unreadable, clean, readback_mode, detail_json, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ReceiptID, v.TransferID, v.PeerID, v.Folder, v.Checked, v.Missing, v.Modified, v.Added, v.Unreadable, v.Clean,
		v.ReadbackMode, v.DetailJSON, Now())
	return err
}
