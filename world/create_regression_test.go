package world

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// create_regression_test.go 针对 Create 本身的失败保障做回归：创建失败后
// 目录原有内容必须原样保留、本次新建的内容必须撤回，排除障碍后可在同一
// 路径继续创建。其余存档用例主要把成功创建当作准备步骤，这里补足创建
// 操作自身的成功与失败约定。

func assertDirExists(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("目录应仍存在 %s: %v", path, err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s 应仍是目录", path)
	}
}

func assertRegularFile(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("普通文件应保留 %s: %v", path, err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("%s 应仍是普通文件", path)
	}
}

// assertCreateFails 断言 Create 明确失败：返回非空错误但不返回句柄，错误
// 信息同时说明失败的操作与具体位置。wantSubs 中每一项都必须出现在错误
// 文本里（如“建立存档子目录”与被占用路径）。
func assertCreateFails(t *testing.T, dir string, wantSubs ...string) {
	t.Helper()
	a, err := Create(dir)
	if err == nil {
		if a != nil {
			t.Fatal("创建失败不应返回存档句柄")
		}
		t.Fatal("Create 应失败但成功了")
	}
	if a != nil {
		t.Fatalf("失败时不应返回句柄，得到 %+v，错误: %v", a, err)
	}
	msg := err.Error()
	for _, sub := range wantSubs {
		if !strings.Contains(msg, sub) {
			t.Fatalf("错误 %q 应说明 %q", msg, sub)
		}
	}
}

// assertCreateSucceedsAndUsable 断言 Create 成功并立即验证返回句柄可用：
// 保存一个合法世界后，按可接受规则版本读回相同的种子、完整规则、时间片
// 及角色物品状态。
func assertCreateSucceedsAndUsable(t *testing.T, dir string) *Archive {
	t.Helper()
	a, err := Create(dir)
	if err != nil {
		t.Fatalf("排除障碍后应能在同一路径创建: %v", err)
	}
	if a == nil {
		t.Fatal("成功创建应返回存档句柄")
	}
	if a.Dir() != dir {
		t.Fatalf("句柄目录应为 %s，得到 %s", dir, a.Dir())
	}

	w := baseWorld(t)
	if _, err := w.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: 1}},
		Time:        5,
	}); err != nil {
		t.Fatalf("准备可保存世界: %v", err)
	}
	info, err := a.Save("slot", w)
	if err != nil {
		t.Fatalf("新建存档应能立即保存: %v", err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("新建存档应能重新打开: %v", err)
	}
	rec, err := reopened.Latest("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("读回已保存世界: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("读回记录标识应为 %s，得到 %s", info.ID, rec.ID)
	}
	got := rec.State
	if got.Seed != 42 || got.Time != 5 || got.Rules.Version != "v1" {
		t.Fatalf("种子/时间片/规则版本读回不符: %+v", got)
	}
	if !reflect.DeepEqual(got.Rules.Locations, []string{"hall", "yard", "cave"}) ||
		len(got.Rules.Edges) != 2 ||
		!reflect.DeepEqual(got.Rules.ItemKinds, []string{"gold", "key"}) ||
		got.Rules.CarryLimits["hero"] != 5 {
		t.Fatalf("完整规则读回不符: %+v", got.Rules)
	}
	if len(got.Characters) != 2 {
		t.Fatalf("角色读回不符: %+v", got.Characters)
	}
	c := got.Characters[0]
	if c.ID != "hero" || c.Location != "yard" ||
		!reflect.DeepEqual(c.Items, []CharacterItem{{Item: "gold", Count: 1}, {Item: "key", Count: 1}}) {
		t.Fatalf("角色位置与物品状态读回不符: %+v", c)
	}
	// 读回的状态可承接为可继续的世界。
	if _, err := WorldFromState(rec.State); err != nil {
		t.Fatalf("读回状态应能重建世界: %v", err)
	}
	return a
}

// TestCreateInMissingDir 目录尚不存在时创建成功，父目录里原有的普通文件
// 不受影响；句柄可立即保存并按可接受规则版本读回完整世界。
func TestCreateInMissingDir(t *testing.T) {
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "game")

	a := assertCreateSucceedsAndUsable(t, dir)
	_ = a

	// 目标目录由本次创建建立：标记、锁文件与两个子目录齐全；父目录里
	// 原有普通文件不受影响；records 中含一条已保存记录（文件名随机）。
	for _, rel := range []string{
		filepath.Join("game", markerName),
		filepath.Join("game", "lock"),
		filepath.Join("game", recordsName),
		filepath.Join("game", slotsName),
		filepath.Join("game", slotsName, "slot.json"),
	} {
		if _, err := os.Stat(filepath.Join(parent, rel)); err != nil {
			t.Fatalf("创建后应存在 %s: %v", rel, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dir, recordsName)); err != nil || len(entries) != 1 {
		t.Fatalf("新建 records 内应含一条已保存记录: %v, %v", entries, err)
	}
	if data, err := os.ReadFile(filepath.Join(parent, "outside.txt")); err != nil || string(data) != "keep me" {
		t.Fatalf("父目录原有普通文件应不变: %q, %v", data, err)
	}
}

// TestCreateReusesExistingEmptySubdirs 目标目录已存在、放有普通文件和可用的
// 空子目录时创建成功：原有普通文件内容保持不变，预先存在的空 records/
// slots 子目录被沿用而非重建，原有的其它空目录也原样保留。
func TestCreateReusesExistingEmptySubdirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("玩家笔记"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, recordsName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, slotsName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "extras"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 在预建 records 目录内放一个标记文件，证明沿用的是同一个目录。
	if err := os.WriteFile(filepath.Join(dir, recordsName, ".keep"), []byte("reuse"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := assertCreateSucceedsAndUsable(t, dir)
	_ = a

	if data, err := os.ReadFile(filepath.Join(dir, "notes.txt")); err != nil || string(data) != "玩家笔记" {
		t.Fatalf("原有普通文件内容应保持不变: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, recordsName, ".keep")); err != nil || string(data) != "reuse" {
		t.Fatalf("原有 records 子目录应被沿用而非重建: %q, %v", data, err)
	}
	assertDirExists(t, filepath.Join(dir, "extras"))
}

// TestCreateFailsWhenSubdirOccupied 准备 records/slots 子目录的位置被普通
// 文件占用时明确失败：错误说明失败操作与具体位置，不返回句柄；本次写入的
// 存档标记被撤回，不留下让下一次创建误以为已有存档的标记，原有占用文件
// 内容与其它原有内容保持原样。
func TestCreateFailsWhenSubdirOccupied(t *testing.T) {
	cases := []struct {
		name        string
		occupied    string
		wantInError string
	}{
		{"records被文件占用", recordsName, recordsName},
		{"slots被文件占用", slotsName, slotsName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			occupy := []byte("我不是目录")
			if err := os.WriteFile(filepath.Join(dir, tc.occupied), occupy, 0o600); err != nil {
				t.Fatal(err)
			}
			// 另一个未被占用的空目录：失败回退不得删它。
			other := slotsName
			if tc.occupied == slotsName {
				other = recordsName
			}
			if err := os.Mkdir(filepath.Join(dir, other), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("保留"), 0o600); err != nil {
				t.Fatal(err)
			}

			assertCreateFails(t, dir, "建立存档子目录", tc.occupied)

			// 本次新建的标记与锁文件被撤回，没有“伪存档”残留。
			if _, err := os.Stat(filepath.Join(dir, markerName)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("失败后应撤回存档标记，stat=%v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "lock")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("失败后不应留下锁文件，stat=%v", err)
			}
			// 占用位置的原有普通文件及其内容保留。
			assertRegularFile(t, filepath.Join(dir, tc.occupied))
			if data, err := os.ReadFile(filepath.Join(dir, tc.occupied)); err != nil || string(data) != string(occupy) {
				t.Fatalf("占用文件内容应保留: %q, %v", data, err)
			}
			// 调用前就存在的空目录与普通文件保留。
			assertDirExists(t, filepath.Join(dir, other))
			if data, err := os.ReadFile(filepath.Join(dir, "notes.txt")); err != nil || string(data) != "保留" {
				t.Fatalf("原有普通文件应保留: %q, %v", data, err)
			}
			assertDirExists(t, dir)

			// 关键回归点：残留标记不得让下一次创建误判为“已是存档”。
			if _, err := Create(dir); err == nil {
				t.Fatal("占用尚未排除，再次创建仍应失败")
			} else if !strings.Contains(err.Error(), "建立存档子目录") {
				t.Fatalf("再次失败应仍是真实的占用障碍，而非存档冲突: %v", err)
			}

			// 排除障碍后在同一路径创建成功，无需手动清理残留。
			if err := os.Remove(filepath.Join(dir, tc.occupied)); err != nil {
				t.Fatal(err)
			}
			assertCreateSucceedsAndUsable(t, dir)
		})
	}
}

// TestCreateFailsWhenLockOccupiedByDir 子目录已建好，但锁位置被同名目录
// 占用而无法建立锁文件时明确失败：错误指出建立锁文件的操作与具体位置，
// 不返回句柄；本次新建的存档标记与子目录全部撤回，占用锁位置的原目录
// 及其内容原样保留，目标目录本身保留。
func TestCreateFailsWhenLockOccupiedByDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("保留"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockDir := filepath.Join(dir, "lock")
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "inside.txt"), []byte("锁目录内容"), 0o600); err != nil {
		t.Fatal(err)
	}

	assertCreateFails(t, dir, "建立存档锁文件", filepath.Join("lock"))

	// 本次新建的存档标记与两个子目录均被撤回。
	for _, name := range []string{markerName, recordsName, slotsName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("失败后应撤回本次新建的 %s，stat=%v", name, err)
		}
	}
	// 占用锁位置的原目录及其内容原样保留。
	assertDirExists(t, lockDir)
	if data, err := os.ReadFile(filepath.Join(lockDir, "inside.txt")); err != nil || string(data) != "锁目录内容" {
		t.Fatalf("占用位置原有内容应保留: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "notes.txt")); err != nil || string(data) != "保留" {
		t.Fatalf("原有普通文件应保留: %q, %v", data, err)
	}
	assertDirExists(t, dir)

	// 排除障碍（移走占用目录）后，同一路径直接创建成功并可正常存取。
	if err := os.RemoveAll(lockDir); err != nil {
		t.Fatal(err)
	}
	assertCreateSucceedsAndUsable(t, dir)
}

// TestCreateRollbackOnlyRemovesNewDirs 当一个子目录是本次新建、另一个在
// 调用前就已存在时，失败后只撤回新建部分：原有的子目录不能一起消失，也
// 不能被清空。
func TestCreateRollbackOnlyRemovesNewDirs(t *testing.T) {
	dir := t.TempDir()
	// records 调用前已存在且非空：必须被沿用并在失败后完整保留。
	if err := os.Mkdir(filepath.Join(dir, recordsName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, recordsName, "old.dat"), []byte("旧内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 锁位置被目录占用：slots 由本次新建、随后在锁步骤失败，用于验证
	// 只撤回新建的 slots 与标记，而调用前已存在的 records 完整保留。
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("保留"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "lock"), 0o755); err != nil {
		t.Fatal(err)
	}

	assertCreateFails(t, dir, "建立存档锁文件")

	// 调用前就存在的 records 目录及其内容原样保留。
	assertDirExists(t, filepath.Join(dir, recordsName))
	if data, err := os.ReadFile(filepath.Join(dir, recordsName, "old.dat")); err != nil || string(data) != "旧内容" {
		t.Fatalf("原有子目录内容不能一起消失或被清空: %q, %v", data, err)
	}
	// 本次新建的 slots 与存档标记被撤回。
	if _, err := os.Stat(filepath.Join(dir, slotsName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("本次新建的 slots 应被撤回，stat=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, markerName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("本次新建的存档标记应被撤回，stat=%v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "notes.txt")); err != nil || string(data) != "保留" {
		t.Fatalf("原有普通文件应保留: %q, %v", data, err)
	}

	if err := os.RemoveAll(filepath.Join(dir, "lock")); err != nil {
		t.Fatal(err)
	}
	assertCreateSucceedsAndUsable(t, dir)
	// 原有 records 目录继续被沿用，旧文件仍在其中。
	if data, err := os.ReadFile(filepath.Join(dir, recordsName, "old.dat")); err != nil || string(data) != "旧内容" {
		t.Fatalf("成功创建后原有 records 内容仍应保留: %q, %v", data, err)
	}
}

// TestCreateExistingArchiveConflict 对本来已经是存档的目录，Create 仍返回
// 现有的 ConflictError 且不提供句柄；原有标记、存档槽和记录保持不变。
func TestCreateExistingArchiveConflict(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	info, err := a.Save("slot", w)
	if err != nil {
		t.Fatal(err)
	}
	markerData, err := os.ReadFile(filepath.Join(dir, markerName))
	if err != nil {
		t.Fatal(err)
	}

	b, err := Create(dir)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("已有存档应返回 ConflictError，得到 %T: %v", err, err)
	}
	if b != nil {
		t.Fatal("冲突时不应返回存档句柄")
	}

	// 原有标记、槽与记录保持不变。
	if data, rErr := os.ReadFile(filepath.Join(dir, markerName)); rErr != nil || !reflect.DeepEqual(data, markerData) {
		t.Fatalf("原有标记应保持不变: %q vs %q, %v", data, markerData, rErr)
	}
	a2, err := Open(dir)
	if err != nil {
		t.Fatalf("原存档应仍可打开: %v", err)
	}
	rec, err := a2.Latest("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("原有槽与记录应保持不变: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("原记录标识应不变: %s -> %s", info.ID, rec.ID)
	}
	slots, err := a2.Slots()
	if err != nil || !reflect.DeepEqual(slots, []string{"slot"}) {
		t.Fatalf("原有槽集合应不变: %v, %v", slots, err)
	}
}

// TestCreateConflictsEvenWhenMarkerUnparseable 即使原有标记无法解析，也应
// 拒绝再次创建：返回 ConflictError、不提供句柄，并把不可读标记的原始内容
// 原样保留——不能因为它不可读就当成本次创建的残留撤回或覆盖。
func TestCreateConflictsEvenWhenMarkerUnparseable(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, markerName)
	garbage := []byte("{这不是合法JSON")
	if err := os.WriteFile(markerPath, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	// 同时放好其余存档结构，模拟一个标记损坏但数据仍在的存档目录。
	if err := os.Mkdir(filepath.Join(dir, recordsName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, slotsName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("留着"), 0o600); err != nil {
		t.Fatal(err)
	}

	a, err := Create(dir)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("不可解析标记仍应返回 ConflictError，得到 %T: %v", err, err)
	}
	if a != nil {
		t.Fatal("冲突时不应返回句柄")
	}

	// 原始标记内容逐字节保留，不被覆盖、截断或删除。
	if data, rErr := os.ReadFile(markerPath); rErr != nil || !reflect.DeepEqual(data, garbage) {
		t.Fatalf("不可解析标记的原始内容应保留: %q, %v", data, rErr)
	}
	if data, rErr := os.ReadFile(filepath.Join(dir, "keep.txt")); rErr != nil || string(data) != "留着" {
		t.Fatalf("其它原有内容应保留: %q, %v", data, rErr)
	}
	// 不应借失败回退新建任何额外结构。
	if _, statErr := os.Stat(filepath.Join(dir, "lock")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("不应为冲突调用新建锁文件，stat=%v", statErr)
	}

	// 持续冲突：修复标记内容前，任何重试都不会把它当残留清掉。
	if _, err := Create(dir); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("再次创建仍应冲突，得到 %T: %v", err, err)
	}
	if data, rErr := os.ReadFile(markerPath); rErr != nil || !reflect.DeepEqual(data, garbage) {
		t.Fatalf("重试后不可解析标记仍应原样保留: %q, %v", data, rErr)
	}
}
