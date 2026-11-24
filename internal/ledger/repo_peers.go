package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Peer is a paired machine whose Ed25519 key is pinned.
type Peer struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	PubKeyB64     string   `json:"pubkey_b64"`
	Addresses     []string `json:"addresses"`
	LastSeen      string   `json:"last_seen,omitempty"`
	LastTransport string   `json:"last_transport,omitempty"`
	PairedAt      string   `json:"paired_at"`
	Revoked       bool     `json:"revoked"`
}

// UpsertPeer pins (or re-pins) a peer.
func UpsertPeer(tx *sql.Tx, p *Peer) error {
	addrs, _ := json.Marshal(p.Addresses)
	if p.PairedAt == "" {
		p.PairedAt = Now()
	}
	_, err := tx.Exec(`INSERT INTO peers (id, name, pubkey_b64, addresses_json, paired_at, revoked) VALUES (?,?,?,?,?,0)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, pubkey_b64=excluded.pubkey_b64, addresses_json=excluded.addresses_json, revoked=0`,
		p.ID, p.Name, p.PubKeyB64, string(addrs), p.PairedAt)
	return err
}

// TouchPeer records the last time and transport a peer was reached.
func TouchPeer(tx *sql.Tx, id, transport string) error {
	_, err := tx.Exec(`UPDATE peers SET last_seen=?, last_transport=? WHERE id=?`, Now(), NullString(transport), id)
	return err
}

// RevokePeer unpins a peer; its key is refused from then on.
func RevokePeer(tx *sql.Tx, id string) error {
	_, err := tx.Exec(`UPDATE peers SET revoked=1 WHERE id=?`, id)
	return err
}

const peerSelect = `SELECT id, name, pubkey_b64, addresses_json, COALESCE(last_seen,''), COALESCE(last_transport,''), paired_at, revoked FROM peers`

func scanPeer(s scanner) (*Peer, error) {
	var p Peer
	var addrs string
	if err := s.Scan(&p.ID, &p.Name, &p.PubKeyB64, &addrs, &p.LastSeen, &p.LastTransport, &p.PairedAt, &p.Revoked); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(addrs), &p.Addresses)
	return &p, nil
}

// GetPeer loads one peer.
func GetPeer(ctx context.Context, q querier, id string) (*Peer, error) {
	p, err := scanPeer(q.QueryRowContext(ctx, peerSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("peer %s: %w", id, ErrNotFound)
	}
	return p, err
}

// ListPeers lists every peer.
func ListPeers(ctx context.Context, q querier) ([]Peer, error) {
	rows, err := q.QueryContext(ctx, peerSelect+` ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	var out []Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, *p)
	}
	return out, closeRows(rows)
}
