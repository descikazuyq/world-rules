package world

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// assertNotExists 断言路径不存在；存在则报告失败，避免把上一次失败
// Create 的残留带到下一次创建。
func assertNotExists(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			t.Fatalf("%s不应在失败后残留: %s", what, path)
		}
		t.Fatalf("检查%s %q: %v", what, path, err)
	}
}

// assertFileContent 断言普通文件存在且内容与 want 完全一致。
func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("原有文件 %q 应保留且可读: %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("原有文件 %q 内容被改动: got %q want %q", path, got, want)
	}
}

// assertUsableArchive 验证句柄可立即保存世界并读回完整状态，且同一
// 路径能由 Open 重新打开——这是一次 Create 成功后应有的使用结果。
func assertUsableArchive(t *testing.T, a *Archive, dir string) {
	t.Helper()
	w := baseWorld(t)
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  3,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatalf("新建存档应能立即保存: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("同一路径应能由 Open 打开: %v", err)
	}
	rec, err := reopened.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应读回刚保存的世界: %v", err)
	}
	snap := w.Snapshot()
	if rec.State.Time != 3 || rec.State.Seed != snap.Seed ||
		rec.State.Characters[0].Location != "yard" {
		t.Fatalf("读回的状态不完整: %+v", rec.State)
	}
}

// TestCreateSubdirOccupiedByFileRollsBack 存放记录或槽信息的子目录位置
// 被同名普通文件占用时，Create 必须报错且不返回句柄，并撤回本次新增的
// 标记与锁文件（子目录位置上的障碍文件不是本次新增，不能删）；用户
// 删除障碍后用同一路径重新创建，应直接得到可用存档，不需要手工清理
// 上一次失败留下的标记。
func TestCreateSubdirOccupiedByFileRollsBack(t *testing.T) {
	for _, sub := range []string{recordsName, slotsName} {
		t.Run(sub, func(t *testing.T) {
			dir := t.TempDir()
			obstacle := filepath.Join(dir, sub)
			if err := os.WriteFile(obstacle, []byte("blocker"), 0o600); err != nil {
				t.Fatal(err)
			}

			a, err := Create(dir)
			if err == nil {
				t.Fatalf("%s 位置被普通文件占用时 Create 应失败", sub)
			}
			if a != nil {
				t.Fatalf("失败时不应返回存档句柄")
			}
			// 本次新增内容全部撤回。
			assertNotExists(t, filepath.Join(dir, markerName), "存档标记")
			assertNotExists(t, filepath.Join(dir, lockName), "锁文件")
			// 障碍文件不是本次新增，保留且内容不变；另一个子目录若已
			// 建立也要随失败撤回。
			assertFileContent(t, obstacle, []byte("blocker"))
			other := slotsName
			if sub == slotsName {
				other = recordsName
			}
			assertNotExists(t, filepath.Join(dir, other), "新建的另一个子目录")

			// 排除障碍后，同一路径重新创建应直接成功并立即可用。
			if err := os.Remove(obstacle); err != nil {
				t.Fatal(err)
			}
			a2, err := Create(dir)
			if err != nil {
				t.Fatalf("排除占用后应能用同一路径重新创建: %v", err)
			}
			assertUsableArchive(t, a2, dir)
		})
	}
}

// TestCreateLockLocationIsDirectory 锁文件应占用的位置已是一个目录时，
// 必须明确拒绝创建：返回错误且不返回句柄，本次新增的标记与子目录全部
// 撤回；锁位置上的原有目录保留。删除该目录后重新创建应成功可用。
func TestCreateLockLocationIsDirectory(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, lockName)
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}

	a, err := Create(dir)
	if err == nil {
		t.Fatal("锁文件位置是目录时 Create 应失败")
	}
	if a != nil {
		t.Fatal("失败时不应返回存档句柄")
	}
	assertNotExists(t, filepath.Join(dir, markerName), "存档标记")
	assertNotExists(t, filepath.Join(dir, recordsName), "records 子目录")
	assertNotExists(t, filepath.Join(dir, slotsName), "slots 子目录")
	// 锁位置上原有的目录不是本次新增，保留不动。
	if fi, statErr := os.Stat(lockDir); statErr != nil || !fi.IsDir() {
		t.Fatalf("原有的锁位置目录应保留: %v", statErr)
	}

	if err := os.Remove(lockDir); err != nil {
		t.Fatal(err)
	}
	a2, err := Create(dir)
	if err != nil {
		t.Fatalf("排除占用后应能重新创建: %v", err)
	}
	assertUsableArchive(t, a2, dir)
}

// TestCreateFailurePreservesExistingContent 回退范围只限本次新增：目标
// 目录调用前已存在时，其中原有的文件、嵌套子目录，以及原本就存在、可供
// 存档使用的空子目录都必须保留且内容不变，目标目录本身也不能被删除。
func TestCreateFailurePreservesExistingContent(t *testing.T) {
	dir := t.TempDir()

	// 调用前已存在的用户内容：普通文件、嵌套子目录及其中文件。
	userFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(userFile, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	nestedDir := filepath.Join(dir, "backup")
	if err := os.Mkdir(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nestedFile := filepath.Join(nestedDir, "old.txt")
	if err := os.WriteFile(nestedFile, []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 调用前就存在、可供存档使用的空 records 子目录。
	emptyRecords := filepath.Join(dir, recordsName)
	if err := os.Mkdir(emptyRecords, 0o755); err != nil {
		t.Fatal(err)
	}
	// slots 位置被占用，制造一次必然失败的创建。
	obstacle := filepath.Join(dir, slotsName)
	if err := os.WriteFile(obstacle, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if a, err := Create(dir); err == nil || a != nil {
		t.Fatalf("被占用的创建应失败且无句柄, a=%v err=%v", a, err)
	}

	// 目标目录与所有原有内容原样保留。
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("调用前已存在的目标目录必须保留: %v", err)
	}
	assertFileContent(t, userFile, []byte("user data"))
	assertFileContent(t, nestedFile, []byte("nested"))
	assertFileContent(t, obstacle, []byte("x"))
	if fi, err := os.Stat(emptyRecords); err != nil || !fi.IsDir() {
		t.Fatalf("原有的空子目录必须保留: %v", err)
	}
	entries, err := os.ReadDir(emptyRecords)
	if err != nil || len(entries) != 0 {
		t.Fatalf("原有空子目录内容不应被改动: %v %d", err, len(entries))
	}
	// 本次新增的标记与锁文件撤回。
	assertNotExists(t, filepath.Join(dir, markerName), "存档标记")
	assertNotExists(t, filepath.Join(dir, lockName), "锁文件")

	// 排除障碍后重新创建成功：沿用原有空 records 目录，存档立即可用。
	if err := os.Remove(obstacle); err != nil {
		t.Fatal(err)
	}
	a2, err := Create(dir)
	if err != nil {
		t.Fatalf("排除占用后应能重新创建: %v", err)
	}
	assertUsableArchive(t, a2, dir)
	// 用户原有数据在成功创建后依旧完好。
	assertFileContent(t, userFile, []byte("user data"))
	assertFileContent(t, nestedFile, []byte("nested"))
}

// TestCreateReusesExistingEmptySubdirs 原本就存在的空子目录可供存档
// 直接使用：创建成功，且句柄保存出的记录确实落在原有目录中。
func TestCreateReusesExistingEmptySubdirs(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{recordsName, slotsName} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a, err := Create(dir)
	if err != nil {
		t.Fatalf("已有空的必要子目录时应直接创建成功: %v", err)
	}
	assertUsableArchive(t, a, dir)
}

// TestCreateExistingMarkerStillConflict 调用前已存在存档标记时按原有
// 规则处理：即使标记无法解析也返回 ConflictError，不覆盖、不删除标记，
// 也不尝试修补或重建目录；再次 Open 不受失败的 Create 影响。
func TestCreateExistingMarkerStillConflict(t *testing.T) {
	dir := t.TempDir()
	// 无法解析的旧标记，并配上必要子目录，模拟一个标记受损的旧存档。
	markerPath := filepath.Join(dir, markerName)
	rawMarker := []byte("{not json")
	if err := os.WriteFile(markerPath, rawMarker, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{recordsName, slotsName} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	a, err := Create(dir)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("已有存档标记时应返回 ConflictError，得到 %T: %v", err, err)
	}
	if a != nil {
		t.Fatal("冲突时不应返回存档句柄")
	}
	// 标记既不被覆盖也不借回退删除，内容逐字节不变。
	assertFileContent(t, markerPath, rawMarker)
	// 冲突路径不新增锁文件或其他内容。
	assertNotExists(t, filepath.Join(dir, lockName), "锁文件")
}

// TestCreateOnExistingUsableArchiveConflict 正常存档上再次调用 Create
// 仍返回冲突错误，存档随后照常可打开、可保存。
func TestCreateOnExistingUsableArchiveConflict(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}

	a2, err := Create(dir)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("已有存档应返回 ConflictError，得到 %T: %v", err, err)
	}
	if a2 != nil {
		t.Fatal("冲突时不应返回句柄")
	}
	// 已有数据完好，仍可打开读取。
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("冲突的 Create 不应影响已有存档: %v", err)
	}
	if _, err := reopened.Latest("s", []string{"v1"}); err != nil {
		t.Fatalf("已有存档数据应完好: %v", err)
	}
}

// TestCreateNestedMissingParents 目标目录及其父目录均不存在时，创建
// 成功并补齐整条路径；失败路径（父级被普通文件挡住）则报错、不返回
// 句柄且不动原有文件。
func TestCreateNestedMissingParents(t *testing.T) {
	root := t.TempDir()

	dir := filepath.Join(root, "a", "b", "archive")
	a, err := Create(dir)
	if err != nil {
		t.Fatalf("应能连同父目录一起建立: %v", err)
	}
	assertUsableArchive(t, a, dir)

	// 父路径中段是普通文件：无法在其下建目录，报错且原文件保留。
	blocked := filepath.Join(root, "x", "archive")
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if a2, err := Create(blocked); err == nil || a2 != nil {
		t.Fatalf("父级被普通文件挡住时应失败且无句柄: %v", err)
	}
	assertFileContent(t, filepath.Join(root, "x"), []byte("file"))
}
