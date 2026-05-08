package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/engine/storage"
)

// fakeStorageEngine is a hand-rolled mock of StorageEngineReader. We don't
// pull in a mocking library — the surface is three methods and using a
// vanilla struct keeps test data declarative.
type fakeStorageEngine struct {
	pools     []storage.Pool
	disks     []storage.Disk
	imports   []storage.ImportablePool
	poolErr   error
	diskErr   error
	importErr error
}

func (f *fakeStorageEngine) ListPools(_ context.Context) ([]storage.Pool, error) {
	return f.pools, f.poolErr
}
func (f *fakeStorageEngine) ListDisks(_ context.Context) ([]storage.Disk, error) {
	return f.disks, f.diskErr
}
func (f *fakeStorageEngine) ListImportable(_ context.Context) ([]storage.ImportablePool, error) {
	return f.imports, f.importErr
}

func samplePools() []storage.Pool {
	return []storage.Pool{
		{
			Name:           "tank",
			GUID:           "12345",
			Health:         storage.HealthOnline,
			SizeBytes:      26388279066624,
			AllocatedBytes: 20126457528320,
			FreeBytes:      6261821538304,
			FragPercent:    12,
			CapPercent:     76,
			Topology: []storage.Vdev{
				{
					Type:   storage.VdevTypeData,
					Layout: storage.LayoutRaidZ2,
					Name:   "raidz2-0",
					Health: storage.HealthOnline,
					Children: []storage.Vdev{
						{Type: storage.VdevTypeData, Name: "/dev/disk/by-id/sda", Path: "/dev/disk/by-id/sda", Health: storage.HealthOnline},
						{Type: storage.VdevTypeData, Name: "/dev/disk/by-id/sdb", Path: "/dev/disk/by-id/sdb", Health: storage.HealthOnline},
						{Type: storage.VdevTypeData, Name: "/dev/disk/by-id/sdc", Path: "/dev/disk/by-id/sdc", Health: storage.HealthOnline},
						{Type: storage.VdevTypeData, Name: "/dev/disk/by-id/sdd", Path: "/dev/disk/by-id/sdd", Health: storage.HealthOnline},
					},
				},
			},
		},
	}
}

func sampleDisks() []storage.Disk {
	return []storage.Disk{
		{Path: "/dev/sda", Model: "WDC WD80EFZZ-68BTXN0", Serial: "VLH8WMTL", SizeBytes: 8001563222016, Pool: "tank", Usage: storage.DiskUsagePool, SMART: storage.SMARTInfo{Status: storage.SMARTStatusPassed, TemperatureC: 38, PowerOnHours: 14820, Source: "smartctl"}},
		{Path: "/dev/sdz", Model: "TOSHIBA HDWG440", Serial: "X1ZYFREE", SizeBytes: 4001000000000, Usage: storage.DiskUsageFree, SMART: storage.SMARTInfo{Status: storage.SMARTStatusPassed, TemperatureC: 35, PowerOnHours: 4012, Source: "smartctl"}},
		{Path: "/dev/sdf", Model: "BAD DISK", Serial: "FAILED01", SizeBytes: 2000000000000, Usage: storage.DiskUsageForeign, SMART: storage.SMARTInfo{Status: storage.SMARTStatusFailed, TemperatureC: 51, PowerOnHours: 28000, Source: "smartctl"}},
		{Path: "/dev/sdusb", Model: "USB STICK", SizeBytes: 32000000000, Usage: storage.DiskUsageFree, SMART: storage.SMARTInfo{Source: "unavailable"}},
	}
}

func sampleImports() []storage.ImportablePool {
	return []storage.ImportablePool{
		{
			Name:  "archive",
			GUID:  "9999",
			State: storage.HealthOnline,
			Topology: []storage.Vdev{
				{Type: storage.VdevTypeData, Layout: storage.LayoutRaidZ1, Name: "raidz1-0", Health: storage.HealthOnline,
					Children: []storage.Vdev{{Path: "/dev/disk/by-id/sdx"}, {Path: "/dev/disk/by-id/sdy"}, {Path: "/dev/disk/by-id/sdz"}}},
			},
		},
	}
}

// [AC-Se3b190-2-1] Storage page renders the disks table with translated SMART
// badges and the pool/usage column.
func TestStoragePage_RendersDisks(t *testing.T) {
	r := newTestRenderer(t)
	r.SetStorageHandler(r.StorageHandler(StorageDeps{Engine: &fakeStorageEngine{
		pools:   samplePools(),
		disks:   sampleDisks(),
		imports: nil,
	}}))
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/storage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := readBody(t, resp)

	// disks table testid
	if !strings.Contains(body, `data-testid="storage-disks"`) {
		t.Errorf("disks section missing")
	}
	// disk row testids
	if strings.Count(body, `data-testid="storage-disk-row"`) != 4 {
		t.Errorf("expected 4 disk rows, body had %d", strings.Count(body, `data-testid="storage-disk-row"`))
	}
	// translated SMART badge labels
	for _, want := range []string{"正常", "異常", "未取得"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing translated SMART badge %q", want)
		}
	}
	// translated usage labels
	for _, want := range []string{"プール内", "未使用", "外部プール"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing usage label %q", want)
		}
	}
	// pool badge for the disk's pool
	if !strings.Contains(body, `<span class="badge">tank</span>`) {
		t.Errorf("body missing pool badge")
	}
}

// [AC-Se3b190-1-1] / [AC-Se3b190-2-1] Storage page renders one PoolCard per
// pool with the topology tree.
func TestStoragePage_RendersPoolsWithTopology(t *testing.T) {
	r := newTestRenderer(t)
	r.SetStorageHandler(r.StorageHandler(StorageDeps{Engine: &fakeStorageEngine{
		pools: samplePools(), disks: sampleDisks(),
	}}))
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/storage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	if strings.Count(body, `data-testid="storage-pool-card"`) != 1 {
		t.Errorf("expected 1 pool card, got %d", strings.Count(body, `data-testid="storage-pool-card"`))
	}
	if !strings.Contains(body, `data-testid="storage-pool-topology"`) {
		t.Errorf("topology block missing")
	}
	// Translated health label "オンライン"
	if !strings.Contains(body, "オンライン") {
		t.Errorf("translated health label missing")
	}
	// Topology summary "raidz2 × 4" (or similar)
	if !strings.Contains(body, "raidz2") {
		t.Errorf("body missing topology layout text")
	}
	// Leaf disk path appears in tree
	if !strings.Contains(body, "/dev/disk/by-id/sda") {
		t.Errorf("body missing leaf disk path")
	}
}

// [AC-Se3b190-3-1] When ListImportable returns ≥1 pool the Storage page shows
// the import banner plus the importable-pools table; the inspect button is
// disabled (next-sprint feature).
func TestStoragePage_ImportBanner(t *testing.T) {
	r := newTestRenderer(t)
	r.SetStorageHandler(r.StorageHandler(StorageDeps{Engine: &fakeStorageEngine{
		pools: samplePools(), disks: sampleDisks(), imports: sampleImports(),
	}}))
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/storage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	if !strings.Contains(body, `data-testid="storage-import-banner"`) {
		t.Errorf("import banner missing")
	}
	if !strings.Contains(body, "archive") {
		t.Errorf("import banner missing pool name")
	}
	if !strings.Contains(body, `data-testid="storage-import"`) {
		t.Errorf("importable-pools table missing")
	}
	if !strings.Contains(body, "実際のインポートは次のスプリントで実装します") {
		t.Errorf("body missing deferred-note text")
	}
	// inspect button must be disabled until next sprint.
	if !strings.Contains(body, `disabled aria-disabled="true">確認`) {
		t.Errorf("inspect button should be disabled with translated label")
	}
}

// [AC-Se3b190-3-1] When ListImportable returns nothing the banner does NOT
// render — verifies negative case, which is what most operators see day-to-day.
func TestStoragePage_NoImportBanner_WhenEmpty(t *testing.T) {
	r := newTestRenderer(t)
	r.SetStorageHandler(r.StorageHandler(StorageDeps{Engine: &fakeStorageEngine{
		pools: samplePools(), disks: sampleDisks(), imports: nil,
	}}))
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/storage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	if strings.Contains(body, `data-testid="storage-import-banner"`) {
		t.Errorf("import banner should be hidden when nothing is importable")
	}
}

// Empty state: no pools, no disks, no imports — page still 200s and shows the
// translated empty-state copy.
func TestStoragePage_EmptyState(t *testing.T) {
	r := newTestRenderer(t)
	r.SetStorageHandler(r.StorageHandler(StorageDeps{Engine: &fakeStorageEngine{}}))
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/storage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `data-testid="storage-empty"`) {
		t.Errorf("empty state placeholder missing")
	}
	if !strings.Contains(body, "プールはまだ構成されていません") {
		t.Errorf("translated empty state copy missing")
	}
}

// formatBytes round-trips known sizes — kept as a separate test since it's
// the most-likely place a typo would hide.
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1500, "1.5 KB"},
		{8001563222016, "8.00 TB"},
		{26388279066624, "26.39 TB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestStoragePage_MethodNotAllowed(t *testing.T) {
	r := newTestRenderer(t)
	r.SetStorageHandler(r.StorageHandler(StorageDeps{Engine: &fakeStorageEngine{}}))
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/ui/admin/storage", "text/plain", strings.NewReader("nope"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}
