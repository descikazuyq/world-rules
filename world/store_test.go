package world

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// tamperChecksum 篡改记录文件的 checksum 字段：文件仍是合法 JSON，
// 但校验和不再匹配（用于模拟可沿父链继续回溯的损坏）。
func tamperChecksum(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := `"checksum": "sha256:`
	idx := strings.Index(string(data), marker)
	if idx < 0 {
		t.Fatal("checksum field not found")
	}
	pos := idx + len(marker)
	if data[pos] == '0' {
		data[pos] = '1'
	} else {
		data[pos] = '0'
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeTestWorld(t *testing.T) *World {
	t.Helper()
	w, err := New(7, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func openTestStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestCreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	w := makeTestWorld(t)
	id, err := s.Create("slot1", w)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("empty record id")
	}

	loaded, recID, err := s.Load("slot1", []string{"v1"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if recID != id {
		t.Fatalf("recID = %q, want %q", recID, id)
	}
	if loaded.Seed() != 7 || loaded.Time() != 0 {
		t.Fatalf("loaded world mismatch: seed=%d time=%d", loaded.Seed(), loaded.Time())
	}
	c, _ := loaded.Character("alice")
	if c.Items["sword"] != 1 {
		t.Fatalf("loaded alice = %+v", c)
	}
}

func TestCreateDuplicateRejected(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	if _, err := s.Create("slot", makeTestWorld(t)); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create("slot", makeTestWorld(t))
	if !errors.Is(err, ErrSlotExists) {
		t.Fatalf("err = %v, want ErrSlotExists", err)
	}
}

func TestOverwriteConflictAndParentChain(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	w := makeTestWorld(t)
	rec1, err := s.Create("slot", w)
	if err != nil {
		t.Fatal(err)
	}

	// 不带期望记录标识直接覆盖 → 冲突。
	if _, err := s.Overwrite("slot", "", w); !errors.Is(err, ErrConflict) {
		t.Fatalf("empty expected: err = %v, want ErrConflict", err)
	}

	// 正常覆盖：rec2 父记录是 rec1。
	w2 := makeTestWorld(t)
	if err := w2.Apply([]Change{{Character: "alice", MoveTo: "B"}}, 1); err != nil {
		t.Fatal(err)
	}
	rec2, err := s.Overwrite("slot", rec1, w2)
	if err != nil {
		t.Fatalf("Overwrite: %v", err)
	}
	if rec2 == rec1 {
		t.Fatal("overwrite produced same record id")
	}

	// 用旧的 rec1 再次覆盖 → 冲突。
	_, err = s.Overwrite("slot", rec1, w2)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale expected: err = %v, want ErrConflict", err)
	}

	// 覆盖不存在的槽 → ErrSlotNotFound。
	_, err = s.Overwrite("nope", rec1, w2)
	if !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("missing slot: err = %v, want ErrSlotNotFound", err)
	}

	// 历史链：rec2 -> rec1，rec1 无父记录。
	hist, err := s.History("slot")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2", len(hist))
	}
	if hist[0].ID != rec2 || hist[0].ParentID != rec1 {
		t.Fatalf("hist[0] = %+v", hist[0])
	}
	if hist[1].ID != rec1 || hist[1].ParentID != "" {
		t.Fatalf("hist[1] = %+v", hist[1])
	}
	if hist[0].Time != 1 || hist[1].Time != 0 {
		t.Fatalf("history times = %d, %d", hist[0].Time, hist[1].Time)
	}

	// 覆盖后旧记录仍可按标识读取。
	old, info, err := s.LoadRecord(rec1, []string{"v1"})
	if err != nil {
		t.Fatalf("LoadRecord rec1: %v", err)
	}
	if info.ParentID != "" || old.Time() != 0 {
		t.Fatalf("rec1 info = %+v, time = %d", info, old.Time())
	}
	latest, _, err := s.LoadRecord(rec2, []string{"v1"})
	if err != nil {
		t.Fatalf("LoadRecord rec2: %v", err)
	}
	if c, _ := latest.Character("alice"); c.Location != "B" {
		t.Fatalf("rec2 alice = %+v", c)
	}
}

func TestBranchCopiesAndIndependent(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	w := makeTestWorld(t)
	rec1, err := s.Create("src", w)
	if err != nil {
		t.Fatal(err)
	}

	// 从最新记录分支。
	branchRec, err := s.Branch("src", "", "branch")
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if branchRec == rec1 {
		t.Fatal("branch record id equals source")
	}

	// 分支完整复制了种子、规则和状态。
	bw, bid, err := s.Load("branch", []string{"v1"})
	if err != nil {
		t.Fatalf("Load branch: %v", err)
	}
	if bid != branchRec {
		t.Fatalf("branch rec = %q, want %q", bid, branchRec)
	}
	if bw.Seed() != 7 || bw.Time() != 0 {
		t.Fatalf("branch world: seed=%d time=%d", bw.Seed(), bw.Time())
	}
	if c, _ := bw.Character("alice"); c.Location != "A" || c.Items["potion"] != 2 {
		t.Fatalf("branch alice = %+v", c)
	}
	binfo, err := s.History("branch")
	if err != nil {
		t.Fatal(err)
	}
	// 新槽首条记录以源记录为父，血缘链延伸到源记录。
	if len(binfo) != 2 {
		t.Fatalf("branch history len = %d, want 2", len(binfo))
	}
	if binfo[0].ID != branchRec || binfo[0].ParentID != rec1 {
		t.Fatalf("branch first record = %+v, want parent %s", binfo[0], rec1)
	}
	if binfo[1].ID != rec1 || binfo[1].ParentID != "" {
		t.Fatalf("branch lineage = %+v, want source record %s", binfo[1], rec1)
	}

	// 源槽继续修改，分支不受影响。
	w2 := makeTestWorld(t)
	if err := w2.Apply([]Change{{Character: "bob", MoveTo: "C"}}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Overwrite("src", rec1, w2); err != nil {
		t.Fatal(err)
	}
	bw2, _, err := s.Load("branch", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := bw2.Character("bob"); c.Location != "B" {
		t.Fatalf("branch bob = %+v, want B (unaffected by src)", c)
	}

	// 分支槽继续修改，源槽不受影响。
	if err := bw.Apply([]Change{{Character: "alice", MoveTo: "B"}}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Overwrite("branch", branchRec, bw); err != nil {
		t.Fatal(err)
	}
	srcW, _, err := s.Load("src", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := srcW.Character("alice"); c.Location != "A" {
		t.Fatalf("src alice = %+v, want A (unaffected by branch)", c)
	}

	// 分支目标槽重名 → 拒绝。
	if _, err := s.Branch("src", "", "branch"); !errors.Is(err, ErrSlotExists) {
		t.Fatalf("duplicate branch: err = %v, want ErrSlotExists", err)
	}
}

func TestBranchFromSpecificHistoryRecord(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	rec1, err := s.Create("src", makeTestWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	w2 := makeTestWorld(t)
	w2.Apply(nil, 4)
	rec2, err := s.Overwrite("src", rec1, w2)
	if err != nil {
		t.Fatal(err)
	}

	// 从旧记录 rec1 分出，分支状态停留在 time=0。
	branchRec, err := s.Branch("src", rec1, "b0")
	if err != nil {
		t.Fatal(err)
	}
	bw, _, err := s.Load("b0", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if bw.Time() != 0 {
		t.Fatalf("branch time = %d, want 0", bw.Time())
	}
	hist, _ := s.History("b0")
	if hist[0].ParentID != rec1 {
		t.Fatalf("branch parent = %q, want %q", hist[0].ParentID, rec1)
	}
	_ = rec2
	_ = branchRec
}

func TestSlots(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	s.Create("b", makeTestWorld(t))
	s.Create("a", makeTestWorld(t))
	s.Create("c", makeTestWorld(t))
	slots, err := s.Slots()
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 3 || slots[0] != "a" || slots[1] != "b" || slots[2] != "c" {
		t.Fatalf("slots = %v", slots)
	}
}

func TestReopen(t *testing.T) {
	dir := t.TempDir()
	{
		s := openTestStore(t, dir)
		w := makeTestWorld(t)
		if _, err := s.Create("slot", w); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	// 重新打开目录，槽位与记录关系仍在。
	s := openTestStore(t, dir)
	defer s.Close()
	slots, err := s.Slots()
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || slots[0] != "slot" {
		t.Fatalf("slots = %v", slots)
	}
	w, _, err := s.Load("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("Load after reopen: %v", err)
	}
	if w.Seed() != 7 {
		t.Fatalf("seed = %d, want 7", w.Seed())
	}
}

func TestCorruptLatestRecover(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	rec1, err := s.Create("slot", makeTestWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	w2 := makeTestWorld(t)
	w2.Apply(nil, 1)
	rec2, err := s.Overwrite("slot", rec1, w2)
	if err != nil {
		t.Fatal(err)
	}
	w3 := makeTestWorld(t)
	w3.Apply(nil, 2)
	rec3, err := s.Overwrite("slot", rec2, w3)
	if err != nil {
		t.Fatal(err)
	}

	// 损坏最新记录 rec3（篡改 checksum：文件仍可解析，校验失败）。
	path := filepath.Join(dir, "records", rec3+".json")
	tamperChecksum(t, path)

	// Load 失败，返回明确的损坏原因。
	if _, _, err := s.Load("slot", []string{"v1"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load err = %v, want ErrCorrupt", err)
	}

	// Recover 找回最近的完整记录 rec2（time=1）。
	rw, rid, err := s.Recover("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rid != rec2 {
		t.Fatalf("recovered = %s, want %s", rid, rec2)
	}
	if rw.Time() != 1 {
		t.Fatalf("recovered time = %d, want 1", rw.Time())
	}

	// 再损坏 rec2，恢复应跳过它找到 rec1。
	path2 := filepath.Join(dir, "records", rec2+".json")
	tamperChecksum(t, path2)
	rw, rid, err = s.Recover("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("Recover after 2 corrupt: %v", err)
	}
	if rid != rec1 || rw.Time() != 0 {
		t.Fatalf("recovered = %s time=%d, want rec1/0", rid, rw.Time())
	}

	// 全部损坏 → 不可恢复。
	path1 := filepath.Join(dir, "records", rec1+".json")
	tamperChecksum(t, path1)
	if _, _, err := s.Recover("slot", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("Recover err = %v, want ErrUnrecoverable", err)
	}
}

func TestVersionRejectedAndRecover(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	rec1, err := s.Create("slot", makeTestWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	// 用 v2 规则的世界覆盖。
	rules2 := testRules()
	rules2.Version = "v2"
	w2, err := New(7, rules2, testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Overwrite("slot", rec1, w2); err != nil {
		t.Fatal(err)
	}

	// 只接受 v1：最新记录版本不被接受，返回明确原因。
	if _, _, err := s.Load("slot", []string{"v1"}); !errors.Is(err, ErrVersion) {
		t.Fatalf("Load err = %v, want ErrVersion", err)
	}
	// 恢复读取找到 v1 旧记录；记录中的规则不被替换。
	rw, rid, err := s.Recover("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rid != rec1 || rw.Rules().Version != "v1" {
		t.Fatalf("recovered = %s version=%s", rid, rw.Rules().Version)
	}
	// 接受 v2 时可以正常读取最新记录。
	rw2, _, err := s.Load("slot", []string{"v2"})
	if err != nil {
		t.Fatalf("Load v2: %v", err)
	}
	if rw2.Rules().Version != "v2" {
		t.Fatalf("version = %q, want v2", rw2.Rules().Version)
	}
	// 只接受不存在的版本 → 不可恢复。
	if _, _, err := s.Recover("slot", []string{"v3"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("Recover v3 err = %v, want ErrUnrecoverable", err)
	}
}

func TestReadsDoNotMutate(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	rec1, err := s.Create("slot", makeTestWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	// 损坏最新记录后，Load 失败不应改动任何文件；Recover 同理。
	recPath := filepath.Join(dir, "records", rec1+".json")
	origManifest, _ := os.ReadFile(filepath.Join(dir, "slots.json"))

	tamperChecksum(t, recPath)
	tampered, _ := os.ReadFile(recPath)
	s.Load("slot", []string{"v1"})
	s.Recover("slot", []string{"v1"})
	s.History("slot")

	after, _ := os.ReadFile(recPath)
	afterManifest, _ := os.ReadFile(filepath.Join(dir, "slots.json"))
	if string(after) != string(tampered) {
		t.Fatal("read operation modified a record file")
	}
	if string(afterManifest) != string(origManifest) {
		t.Fatal("read operation modified the manifest")
	}
}

func TestTornWriteIgnored(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	defer s.Close()

	if _, err := s.Create("slot", makeTestWorld(t)); err != nil {
		t.Fatal(err)
	}
	// 模拟中断后残留的临时文件：重新打开只能看到完整记录。
	tmp := filepath.Join(dir, "records", ".slots.json.tmp.abc123")
	if err := os.WriteFile(tmp, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, _, err := s.Load("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("Load with leftover tmp: %v", err)
	}
	if w.Seed() != 7 {
		t.Fatalf("seed = %d", w.Seed())
	}
}

func TestConcurrentOverwriteOnlyOneWins(t *testing.T) {
	dir := t.TempDir()
	// 先建立槽位。
	s0 := openTestStore(t, dir)
	rec1, err := s0.Create("slot", makeTestWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	s0.Close()

	const n = 12
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	conflicts := 0
	var other []error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := openTestStore(t, dir)
			defer s.Close()
			// 每个实例都读到相同的最新记录标识，然后尝试覆盖同一父记录。
			_, err := s.Overwrite("slot", rec1, makeTestWorld(t))
			mu.Lock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrConflict):
				conflicts++
			default:
				other = append(other, err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("wins=%d conflicts=%d other=%v, want 1/%d", wins, conflicts, other, n-1)
	}

	// 最终链完整：latest 的父记录是 rec1，且历史中只有一条新记录。
	s := openTestStore(t, dir)
	defer s.Close()
	hist, err := s.History("slot")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2", len(hist))
	}
	if hist[0].ParentID != rec1 {
		t.Fatalf("latest parent = %q, want %q", hist[0].ParentID, rec1)
	}
}

func TestConcurrentCreateSameSlotOnlyOneWins(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, dup := 0, 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := openTestStore(t, dir)
			defer s.Close()
			_, err := s.Create("same", makeTestWorld(t))
			mu.Lock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrSlotExists):
				dup++
			default:
				t.Errorf("unexpected err: %v", err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if wins != 1 || dup != n-1 {
		t.Fatalf("wins=%d dup=%d, want 1/%d", wins, dup, n-1)
	}
}
