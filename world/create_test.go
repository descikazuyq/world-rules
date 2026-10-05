package world

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// advancedWorld 返回一个已推进到非零时间片、角色有位置和物品的合法世界，
// 用于验证创建成功后句柄能立即保存并完整读回。
func advancedWorld(t *testing.T) *World {
	t.Helper()
	w := baseWorld(t)
	if _, err := w.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 2}},
		Time:        5,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return w
}

// saveAndReadBack 用句柄保存世界并按可接受规则版本读回，要求种子、完整
// 规则、时间片及角色物品状态与保存时完全一致。
func saveAndReadBack(t *testing.T, a *Archive, w *World) {
	t.Helper()
	want := w.Snapshot()
	info, err := a.Save("main", w)
	if err != nil {
		t.Fatalf("创建成功后应能立即保存: %v", err)
	}
	if !info.SlotFirst || info.Parent != "" {
		t.Fatalf("首条记录关系错误: %+v", info)
	}
	rec, err := a.Latest("main", []string{"v1"})
	if err != nil {
		t.Fatalf("按可接受版本读回: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("读回的记录标识应为 %s，得到 %s", info.ID, rec.ID)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存时不一致:\n保存: %+v\n读回: %+v", want, rec.State)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s 不应存在（err=%v）", path, err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s 内容应为 %q，得到 %q", path, want, data)
	}
}

func assertIsDir(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		t.Fatalf("%s 应是已存在的目录（fi=%v, err=%v）", path, fi, err)
	}
}

// TestCreateFreshDirSaveAndReadBack 在尚不存在的路径上创建：目录被建立，
// 返回的句柄可立即保存合法世界并按可接受规则版本读回完整状态。
func TestCreateFreshDirSaveAndReadBack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "archive")
	a, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a == nil {
		t.Fatal("成功时应返回句柄")
	}
	for _, name := range []string{markerName, recordsName, slotsName, "lock"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("创建后应存在 %s: %v", name, err)
		}
	}
	saveAndReadBack(t, a, advancedWorld(t))
}

// TestCreateExistingDirPreservesContent 在已存在、放有普通文件和可用空
// 子目录的目录上创建：普通文件内容不变，原有空 records/slots 子目录被
// 沿用，创建成功后正常保存与读回。
func TestCreateExistingDirPreservesContent(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("别动我"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{recordsName, slotsName} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	a, err := Create(dir)
	if err != nil {
		t.Fatalf("已有普通文件与空子目录不应妨碍创建: %v", err)
	}
	assertFileContent(t, keep, "别动我")
	assertIsDir(t, filepath.Join(dir, recordsName))
	assertIsDir(t, filepath.Join(dir, slotsName))
	saveAndReadBack(t, a, advancedWorld(t))
}

// TestCreateFailsWhenSubdirOccupied 准备 records/slots 子目录的位置被普通
// 文件占用时创建明确失败：错误指明失败操作与位置、不返回句柄；本次新建的
// 存档标记与已建好的子目录被撤回，目录原有内容与占用文件保持原样；排除
// 占用后可在同一路径重新创建并正常保存读取。
func TestCreateFailsWhenSubdirOccupied(t *testing.T) {
	for _, occupied := range []string{recordsName, slotsName} {
		t.Run(occupied, func(t *testing.T) {
			dir := t.TempDir()
			keep := filepath.Join(dir, "keep.txt")
			if err := os.WriteFile(keep, []byte("原有内容"), 0o600); err != nil {
				t.Fatal(err)
			}
			blocker := filepath.Join(dir, occupied)
			if err := os.WriteFile(blocker, []byte("占用位置"), 0o600); err != nil {
				t.Fatal(err)
			}

			a, err := Create(dir)
			if err == nil {
				t.Fatal("子目录位置被普通文件占用时应失败")
			}
			if a != nil {
				t.Fatal("失败时不应返回句柄")
			}
			if !strings.Contains(err.Error(), blocker) {
				t.Fatalf("错误应指明具体位置 %s: %v", blocker, err)
			}
			if !strings.Contains(err.Error(), "子目录") {
				t.Fatalf("错误应说明失败的操作（建立子目录）: %v", err)
			}

			// 本次新建的标记被撤回，不会误导下一次创建。
			assertAbsent(t, filepath.Join(dir, markerName))
			// 本次新建的子目录也被撤回：占用 records 时 slots 尚未建立；
			// 占用 slots 时 records 是本次新建，应一并撤回。
			other := slotsName
			if occupied == slotsName {
				other = recordsName
			}
			assertAbsent(t, filepath.Join(dir, other))
			// 调用前已有的普通文件与占用文件保持原样。
			assertFileContent(t, keep, "原有内容")
			assertFileContent(t, blocker, "占用位置")

			// 排除占用障碍后同一路径可重新创建，无需清理失败残留。
			if err := os.Remove(blocker); err != nil {
				t.Fatal(err)
			}
			a2, err := Create(dir)
			if err != nil {
				t.Fatalf("排除障碍后应能在同一路径创建: %v", err)
			}
			assertFileContent(t, keep, "原有内容")
			saveAndReadBack(t, a2, advancedWorld(t))
		})
	}
}

// TestCreateFailsWhenLockOccupied 已准备部分存档结构（标记与新建子目录）
// 后锁位置被目录占用时创建明确失败：错误指明操作与位置、不返回句柄；回退
// 只撤回本次新建的标记与子目录——调用前已存在的子目录及其内容、占用锁位置
// 的目录都保持原样；排除障碍后同一路径可重新创建。
func TestCreateFailsWhenLockOccupied(t *testing.T) {
	dir := t.TempDir()
	// records 在调用前已存在且非空：回退时不能一起消失或被清空。
	preRecords := filepath.Join(dir, recordsName)
	if err := os.Mkdir(preRecords, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(preRecords, "inner.txt")
	if err := os.WriteFile(inner, []byte("原有子目录内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 锁位置被目录占用。
	lockPath := filepath.Join(dir, "lock")
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}

	a, err := Create(dir)
	if err == nil {
		t.Fatal("锁位置被目录占用时应失败")
	}
	if a != nil {
		t.Fatal("失败时不应返回句柄")
	}
	if !strings.Contains(err.Error(), lockPath) {
		t.Fatalf("错误应指明具体位置 %s: %v", lockPath, err)
	}
	if !strings.Contains(err.Error(), "锁") {
		t.Fatalf("错误应说明失败的操作（建立锁文件）: %v", err)
	}

	// 本次新建的标记与 slots 子目录被撤回。
	assertAbsent(t, filepath.Join(dir, markerName))
	assertAbsent(t, filepath.Join(dir, slotsName))
	// 调用前已存在的 records 子目录及其内容保持原样，占用目录也保留。
	assertIsDir(t, preRecords)
	assertFileContent(t, inner, "原有子目录内容")
	assertIsDir(t, lockPath)

	// 排除占用后同一路径重新创建成功，原有内容仍在，可正常保存读取。
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	a2, err := Create(dir)
	if err != nil {
		t.Fatalf("排除障碍后应能在同一路径创建: %v", err)
	}
	assertFileContent(t, inner, "原有子目录内容")
	saveAndReadBack(t, a2, advancedWorld(t))
}

// TestCreateOnExistingArchiveConflict 对已经是存档的目录，Create 返回现有
// 冲突错误且不提供句柄；原有标记、存档槽与记录保持原样。
func TestCreateOnExistingArchiveConflict(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	markerPath := filepath.Join(dir, markerName)
	markerBefore, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	recordsBefore := recordCount(t, a)

	a2, err := Create(dir)
	if err == nil {
		t.Fatal("已是存档的目录应拒绝再次创建")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("应返回 ConflictError，得到 %T: %v", err, err)
	}
	if a2 != nil {
		t.Fatal("冲突时不应返回句柄")
	}

	// 原有标记、存档槽与记录保持原样，仍可按原样打开读取。
	assertFileContent(t, markerPath, string(markerBefore))
	if got := recordCount(t, a); got != recordsBefore {
		t.Fatalf("记录数量不应改变: %d -> %d", recordsBefore, got)
	}
	slots, err := a.Slots()
	if err != nil || len(slots) != 1 || slots[0] != "s" {
		t.Fatalf("存档槽应保持原样: %v, %v", slots, err)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil || rec.ID != ids[0] {
		t.Fatalf("原有记录应仍可读取: %v, %+v", err, rec)
	}
}

// TestCreateUnparseableMarkerStillConflict 已有标记即使无法解析，Create 仍
// 拒绝再次创建并保留其原始内容，不把它当成本次创建的残留撤回或覆盖。
func TestCreateUnparseableMarkerStillConflict(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, markerName)
	garbage := "{这不是合法的存档标记"
	if err := os.WriteFile(markerPath, []byte(garbage), 0o600); err != nil {
		t.Fatal(err)
	}

	a, err := Create(dir)
	if err == nil {
		t.Fatal("已有无法解析的标记时仍应拒绝创建")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("应返回 ConflictError，得到 %T: %v", err, err)
	}
	if a != nil {
		t.Fatal("冲突时不应返回句柄")
	}
	// 原始标记内容逐字节保留：既不覆盖也不借回退删除。
	assertFileContent(t, markerPath, garbage)
}
