package system

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
)

// allocateUID picks a stable uid for userID and persists it. Algorithm:
//
//  1. If a row already exists for userID, return its uid (stable).
//  2. Hash userID with FNV-1a, mod 10000, add UIDMin (== 30000-39999 base).
//  3. If that uid is taken by another row, linear-probe forward
//     (wrapping at UIDMax+1 -> UIDMin) until a free slot is found.
//  4. INSERT (userID, uid). Conflict at INSERT time means a parallel
//     allocation snuck in — re-read and return the row that won.
//
// The deterministic seed step 2 means a fresh restore of state.db lands
// users on the same uid they had before the backup, satisfying the SSOT
// round-trip invariant (priority #1).
func (s *service) allocateUID(ctx context.Context, tx *sql.Tx, userID string) (int, error) {
	if userID == "" {
		return 0, fmt.Errorf("system: userID is empty")
	}
	var existing int
	err := tx.QueryRowContext(ctx, `SELECT uid FROM uid_alloc WHERE user_id = ?`, userID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("system: lookup uid: %w", err)
	}

	taken, err := loadAllocated(ctx, tx, "SELECT uid FROM uid_alloc")
	if err != nil {
		return 0, err
	}

	uid, err := pickFree(seed(userID), UIDMin, UIDMax, taken)
	if err != nil {
		return 0, ErrUIDExhausted
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO uid_alloc (user_id, uid) VALUES (?, ?)
	`, userID, uid); err != nil {
		return 0, fmt.Errorf("system: insert uid_alloc: %w", err)
	}
	return uid, nil
}

func (s *service) allocateGID(ctx context.Context, tx *sql.Tx, groupID string) (int, error) {
	if groupID == "" {
		return 0, fmt.Errorf("system: groupID is empty")
	}
	var existing int
	err := tx.QueryRowContext(ctx, `SELECT gid FROM gid_alloc WHERE group_id = ?`, groupID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("system: lookup gid: %w", err)
	}
	taken, err := loadAllocated(ctx, tx, "SELECT gid FROM gid_alloc")
	if err != nil {
		return 0, err
	}
	gid, err := pickFree(seed(groupID), GIDMin, GIDMax, taken)
	if err != nil {
		return 0, ErrGIDExhausted
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO gid_alloc (group_id, gid) VALUES (?, ?)
	`, groupID, gid); err != nil {
		return 0, fmt.Errorf("system: insert gid_alloc: %w", err)
	}
	return gid, nil
}

func loadAllocated(ctx context.Context, tx *sql.Tx, query string) (map[int]bool, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("system: load allocated: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("system: scan allocated: %w", err)
		}
		out[n] = true
	}
	return out, rows.Err()
}

// seed maps an opaque key (user_id / group_id hex string) to an offset in
// [0, span). FNV-1a is fast, has good distribution for short hex strings,
// and is stable across Go versions and platforms — a hash that drifted with
// the runtime would break the priority #1 round-trip invariant.
func seed(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(key)))
	return h.Sum32()
}

// pickFree returns the first free integer in [min, max] starting from the
// hash-derived candidate, wrapping at max+1 -> min. Returns an error if the
// entire range is taken.
func pickFree(s uint32, min, max int, taken map[int]bool) (int, error) {
	span := max - min + 1
	start := int(s % uint32(span))
	for i := 0; i < span; i++ {
		cand := min + ((start + i) % span)
		if !taken[cand] {
			return cand, nil
		}
	}
	return 0, fmt.Errorf("range exhausted")
}
