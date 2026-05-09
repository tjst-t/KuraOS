package system

import (
	"context"
	"fmt"
	"testing"
)

// [AC-Ssys001-1-1] uid allocation lands in the 30000-39999 range,
// persists across calls (stable per user_id), and increments without
// collision when many users are created.
func TestAllocateUID_Allocates(t *testing.T) {
	ctx := context.Background()
	eng, _ := newTestEngine(t)

	cases := []struct {
		name    string
		userID  string
		wantMin int
		wantMax int
	}{
		{"first user", "u-alice-0001", UIDMin, UIDMax},
		{"second user", "u-bob-0002", UIDMin, UIDMax},
		{"third user", "u-carol-0003", UIDMin, UIDMax},
	}
	seen := map[int]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uid, err := eng.AllocateUID(ctx, c.userID)
			if err != nil {
				t.Fatalf("AllocateUID: %v", err)
			}
			if uid < c.wantMin || uid > c.wantMax {
				t.Fatalf("uid %d outside [%d, %d]", uid, c.wantMin, c.wantMax)
			}
			if seen[uid] {
				t.Fatalf("uid %d collision", uid)
			}
			seen[uid] = true
		})
	}
}

// [AC-Ssys001-1-2] Same user_id must always resolve to the same uid even
// after restart-equivalent (re-open of the engine on the same DB).
func TestAllocateUID_DeterministicNoCollision(t *testing.T) {
	ctx := context.Background()
	eng, st := newTestEngine(t)

	uid1, err := eng.AllocateUID(ctx, "u-alice")
	if err != nil {
		t.Fatalf("AllocateUID: %v", err)
	}
	uid2, err := eng.AllocateUID(ctx, "u-alice")
	if err != nil {
		t.Fatalf("AllocateUID re-call: %v", err)
	}
	if uid1 != uid2 {
		t.Fatalf("uid drifted: %d -> %d", uid1, uid2)
	}

	// Re-open the engine on the same DB; same uid must come back.
	eng2 := newEngineOnStore(t, st)
	uid3, err := eng2.AllocateUID(ctx, "u-alice")
	if err != nil {
		t.Fatalf("AllocateUID after re-open: %v", err)
	}
	if uid3 != uid1 {
		t.Fatalf("uid not stable across re-open: %d != %d", uid3, uid1)
	}
}

// [AC-Ssys001-1-2] Collision avoidance: when many user_ids hash to the
// same starting bucket, linear probing must hand out distinct uids.
func TestAllocateUID_CollisionResolves(t *testing.T) {
	ctx := context.Background()
	eng, _ := newTestEngine(t)

	const N = 200
	seen := map[int]bool{}
	for i := 0; i < N; i++ {
		uid, err := eng.AllocateUID(ctx, fmt.Sprintf("u-%05d", i))
		if err != nil {
			t.Fatalf("AllocateUID %d: %v", i, err)
		}
		if seen[uid] {
			t.Fatalf("uid %d duplicated at iteration %d", uid, i)
		}
		seen[uid] = true
		if uid < UIDMin || uid > UIDMax {
			t.Fatalf("uid %d outside KuraOS range", uid)
		}
	}
	if got := len(seen); got != N {
		t.Fatalf("expected %d unique uids, got %d", N, got)
	}
}

// pickFree must return ErrUIDExhausted-style error when range is full.
func TestPickFree_Exhausted(t *testing.T) {
	taken := map[int]bool{}
	for i := UIDMin; i <= UIDMax; i++ {
		taken[i] = true
	}
	if _, err := pickFree(0, UIDMin, UIDMax, taken); err == nil {
		t.Fatalf("expected exhaustion error")
	}
}
