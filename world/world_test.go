package world

import (
	"testing"
)

func testRules() Rules {
	return Rules{
		Version:    "v1",
		Locations:  []string{"A", "B", "C"},
		Items:      []string{"sword", "shield", "potion"},
		CarryLimit: 10,
	}
}

func testLocations() []Location {
	return []Location{
		{ID: "A", Connections: []string{"B"}},
		{ID: "B", Connections: []string{"A", "C"}},
		{ID: "C", Connections: []string{"A"}},
	}
}

func testCharacters() []Character {
	return []Character{
		{ID: "alice", Location: "A", Items: map[string]int{"sword": 1, "potion": 2}},
		{ID: "bob", Location: "B", Items: map[string]int{"shield": 1}},
	}
}

func TestNewValid(t *testing.T) {
	w, err := New(42, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.Seed() != 42 {
		t.Fatalf("seed = %d, want 42", w.Seed())
	}
	if w.Time() != 0 {
		t.Fatalf("time = %d, want 0", w.Time())
	}
	r := w.Rules()
	if r.Version != "v1" || r.CarryLimit != 10 {
		t.Fatalf("rules = %+v", r)
	}
	if c, ok := w.Character("alice"); !ok || c.Location != "A" || c.Items["potion"] != 2 {
		t.Fatalf("alice = %+v, ok=%v", c, ok)
	}
	if l, ok := w.Location("B"); !ok || len(l.Connections) != 2 {
		t.Fatalf("B = %+v, ok=%v", l, ok)
	}
}

func TestNewInvalid(t *testing.T) {
	good := testRules()
	goodLocs := testLocations()
	goodChars := testCharacters()

	cases := []struct {
		name  string
		rules Rules
		locs  []Location
		chars []Character
	}{
		{"empty version", func() Rules { r := good; r.Version = ""; return r }(), goodLocs, goodChars},
		{"negative carry limit", func() Rules { r := good; r.CarryLimit = -1; return r }(), goodLocs, goodChars},
		{"duplicate location id", good,
			[]Location{{ID: "A"}, {ID: "A"}}, goodChars},
		{"duplicate character id", good, goodLocs,
			[]Character{{ID: "x", Location: "A"}, {ID: "x", Location: "B"}}},
		{"edge to missing location", good,
			[]Location{{ID: "A", Connections: []string{"Z"}}}, goodChars},
		{"character at missing location", good, goodLocs,
			[]Character{{ID: "x", Location: "Z"}}},
		{"negative item count", good, goodLocs,
			[]Character{{ID: "x", Location: "A", Items: map[string]int{"sword": -1}}}},
		{"item not allowed", good, goodLocs,
			[]Character{{ID: "x", Location: "A", Items: map[string]int{"gun": 1}}}},
		{"over carry limit", good, goodLocs,
			[]Character{{ID: "x", Location: "A", Items: map[string]int{"sword": 11}}}},
		{"location not in rules", good,
			[]Location{{ID: "Z"}}, goodChars},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(1, tc.rules, tc.locs, tc.chars); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestNewDoesNotMutateInputs(t *testing.T) {
	rules := testRules()
	locs := testLocations()
	chars := testCharacters()
	w, err := New(1, rules, locs, chars)
	if err != nil {
		t.Fatal(err)
	}
	// 修改输入不影响世界。
	rules.CarryLimit = 999
	locs[0].Connections[0] = "C"
	chars[0].Items["sword"] = 99
	if c, _ := w.Character("alice"); c.Items["sword"] != 1 {
		t.Fatal("world mutated through inputs")
	}
}

func TestApplyMove(t *testing.T) {
	w, err := New(1, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Apply([]Change{{Character: "alice", MoveTo: "B"}}, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if c, _ := w.Character("alice"); c.Location != "B" {
		t.Fatalf("alice location = %q, want B", c.Location)
	}
}

func TestApplyMoveAlongEdgeOnly(t *testing.T) {
	w, err := New(1, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	// C -> A 有边，A -> C 没有边。
	if err := w.Apply([]Change{{Character: "alice", MoveTo: "C"}}, 0); err == nil {
		t.Fatal("expected error for move along non-edge, got nil")
	}
	if c, _ := w.Character("alice"); c.Location != "A" {
		t.Fatalf("alice location = %q, want A (unchanged)", c.Location)
	}
}

func TestApplyItems(t *testing.T) {
	w, err := New(1, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	// alice: sword 1 + potion 2 = 3；加 7 sword 到 10，再加 1 应失败。
	if err := w.Apply([]Change{{Character: "alice", Items: map[string]int{"sword": 7}}}, 0); err != nil {
		t.Fatalf("Apply +7: %v", err)
	}
	if err := w.Apply([]Change{{Character: "alice", Items: map[string]int{"sword": 1}}}, 0); err == nil {
		t.Fatal("expected over-limit error, got nil")
	}
	if c, _ := w.Character("alice"); c.Items["sword"] != 8 {
		t.Fatalf("sword = %d, want 8 (failed apply must not change state)", c.Items["sword"])
	}
	// 减少到 0 合法；再减为负失败。
	if err := w.Apply([]Change{{Character: "alice", Items: map[string]int{"potion": -2}}}, 0); err != nil {
		t.Fatalf("Apply -2 potion: %v", err)
	}
	if err := w.Apply([]Change{{Character: "alice", Items: map[string]int{"potion": -1}}}, 0); err == nil {
		t.Fatal("expected negative count error, got nil")
	}
}

func TestApplyUnknownCharacterOrItem(t *testing.T) {
	w, err := New(1, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Apply([]Change{{Character: "nobody", MoveTo: "B"}}, 0); err == nil {
		t.Fatal("expected unknown character error")
	}
	if err := w.Apply([]Change{{Character: "alice", Items: map[string]int{"gun": 1}}}, 0); err == nil {
		t.Fatal("expected disallowed item error")
	}
	if err := w.Apply([]Change{{Character: "alice", MoveTo: "nowhere"}}, 0); err == nil {
		t.Fatal("expected unknown location error")
	}
}

func TestApplyTime(t *testing.T) {
	w, err := New(1, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Apply(nil, 3); err != nil {
		t.Fatal(err)
	}
	if w.Time() != 3 {
		t.Fatalf("time = %d, want 3", w.Time())
	}
	if err := w.Apply(nil, -1); err == nil {
		t.Fatal("expected backwards-time error")
	}
	if w.Time() != 3 {
		t.Fatalf("time = %d, want 3 (unchanged)", w.Time())
	}
}

func TestApplyAtomicBatch(t *testing.T) {
	w, err := New(1, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	// 一批中 alice 移动成功，但 bob 携带超限，整批失败，两人状态都不变。
	changes := []Change{
		{Character: "alice", MoveTo: "B"},
		{Character: "bob", Items: map[string]int{"sword": 100}},
	}
	if err := w.Apply(changes, 2); err == nil {
		t.Fatal("expected batch failure")
	}
	if c, _ := w.Character("alice"); c.Location != "A" {
		t.Fatalf("alice moved after failed batch: %q", c.Location)
	}
	if c, _ := w.Character("bob"); c.Items["sword"] != 0 {
		t.Fatalf("bob changed after failed batch: %+v", c.Items)
	}
	if w.Time() != 0 {
		t.Fatalf("time = %d, want 0 after failed batch", w.Time())
	}
}

func TestSnapshotIsolation(t *testing.T) {
	w, err := New(1, testRules(), testLocations(), testCharacters())
	if err != nil {
		t.Fatal(err)
	}
	snap := w.Snapshot()
	snap.Characters[0].Items["sword"] = 999
	snap.Characters[0].Location = "C"
	snap.Rules.CarryLimit = 1
	if c, _ := w.Character("alice"); c.Items["sword"] != 1 || c.Location != "A" {
		t.Fatal("world mutated through snapshot")
	}
	// 再次读取的副本也互不影响。
	c1, _ := w.Character("alice")
	c1.Items["sword"] = 5
	c2, _ := w.Character("alice")
	if c2.Items["sword"] != 1 {
		t.Fatal("character copies share state")
	}
}

func TestReadyStillWorks(t *testing.T) {
	if !Ready() {
		t.Fatal("baseline not ready")
	}
}
