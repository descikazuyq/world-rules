// Package world 是本地世界规则与存档的本地基线。
package world

import (
	"errors"
	"fmt"
	"sort"
)

// Rules 描述世界规则。
type Rules struct {
	// Version 是非空的规则版本标识。
	Version string
	// Locations 是规则允许的地点标识集合。
	Locations []string
	// Items 是规则允许的物品种类集合。
	Items []string
	// CarryLimit 是每个角色可携带物品总量的上限，不能为负。
	CarryLimit int
}

// Location 是地点及其连通关系。连通关系是有向边：
// 角色可以沿边从本地点前往 Connections 中列出的地点。
type Location struct {
	ID          string
	Connections []string
}

// Character 是角色及其当前状态。
type Character struct {
	ID       string
	Location string
	// Items 是物品标识到数量的映射，数量必须为非负整数。
	Items map[string]int
}

// Change 是一次原子提交中的单条变更。
type Change struct {
	// Character 是变更所属的角色标识。
	Character string
	// MoveTo 是要前往的地点；为空表示不移动。
	MoveTo string
	// Items 是物品数量变化，正增负减；为空表示不变化。
	Items map[string]int
}

// WorldData 是世界的完整快照，用于存档与恢复。
type WorldData struct {
	Seed       int
	Rules      Rules
	Locations  []Location
	Characters []Character
	// Time 是时间片，新世界从 0 开始。
	Time int
}

type locState struct {
	id    string
	edges map[string]bool
}

type charState struct {
	id    string
	loc   string
	items map[string]int
}

// World 是受规则约束的世界状态。
//
// 所有修改都通过 Apply 进行；读取状态的方法返回的都是副本，
// 调用方修改副本不会影响世界，也不会影响已保存的存档记录。
type World struct {
	seed  int
	rules Rules
	locs  map[string]*locState
	chars map[string]*charState
	time  int
}

// New 建立世界。任何初始数据违反规则都返回错误，且不产生世界。
func New(seed int, rules Rules, locations []Location, characters []Character) (*World, error) {
	if rules.Version == "" {
		return nil, errors.New("world: 规则版本不能为空")
	}
	if rules.CarryLimit < 0 {
		return nil, errors.New("world: 携带上限不能为负")
	}

	allowedLocs := make(map[string]bool, len(rules.Locations))
	for _, l := range rules.Locations {
		allowedLocs[l] = true
	}
	allowedItems := make(map[string]bool, len(rules.Items))
	for _, it := range rules.Items {
		allowedItems[it] = true
	}

	w := &World{
		seed: seed,
		rules: Rules{
			Version:    rules.Version,
			Locations:  append([]string(nil), rules.Locations...),
			Items:      append([]string(nil), rules.Items...),
			CarryLimit: rules.CarryLimit,
		},
		locs:  make(map[string]*locState, len(locations)),
		chars: make(map[string]*charState, len(characters)),
	}

	for _, l := range locations {
		if l.ID == "" {
			return nil, errors.New("world: 地点标识不能为空")
		}
		if _, exists := w.locs[l.ID]; exists {
			return nil, fmt.Errorf("world: 地点标识重复: %q", l.ID)
		}
		if !allowedLocs[l.ID] {
			return nil, fmt.Errorf("world: 地点 %q 不在规则允许的地点中", l.ID)
		}
		ls := &locState{id: l.ID, edges: make(map[string]bool, len(l.Connections))}
		for _, e := range l.Connections {
			if !allowedLocs[e] {
				return nil, fmt.Errorf("world: 地点 %q 的连通关系引用了规则不允许的地点 %q", l.ID, e)
			}
			ls.edges[e] = true
		}
		w.locs[l.ID] = ls
	}
	for id, ls := range w.locs {
		for e := range ls.edges {
			if _, ok := w.locs[e]; !ok {
				return nil, fmt.Errorf("world: 地点 %q 的连通关系引用了不存在的地点 %q", id, e)
			}
		}
	}

	for _, c := range characters {
		if c.ID == "" {
			return nil, errors.New("world: 角色标识不能为空")
		}
		if _, exists := w.chars[c.ID]; exists {
			return nil, fmt.Errorf("world: 角色标识重复: %q", c.ID)
		}
		if _, ok := w.locs[c.Location]; !ok {
			return nil, fmt.Errorf("world: 角色 %q 位于不存在的地点 %q", c.ID, c.Location)
		}
		cs := &charState{
			id:    c.ID,
			loc:   c.Location,
			items: make(map[string]int, len(c.Items)),
		}
		total := 0
		for it, n := range c.Items {
			if !allowedItems[it] {
				return nil, fmt.Errorf("world: 角色 %q 携带了规则不允许的物品 %q", c.ID, it)
			}
			if n < 0 {
				return nil, fmt.Errorf("world: 角色 %q 的物品 %q 数量为负", c.ID, it)
			}
			total += n
			cs.items[it] = n
		}
		if total > rules.CarryLimit {
			return nil, fmt.Errorf("world: 角色 %q 携带物品总量 %d 超过上限 %d", c.ID, total, rules.CarryLimit)
		}
		w.chars[c.ID] = cs
	}

	return w, nil
}

// Apply 一次提交多个角色的移动、物品数量变化和时间推进。
//
// 提交在世界的副本上进行校验：任意一项违反规则，整个提交失败，
// 世界保持提交前的状态。advance 为时间片推进量，不能为负（时间不能倒退）。
func (w *World) Apply(changes []Change, advance int) error {
	if advance < 0 {
		return errors.New("world: 时间不能倒退")
	}

	draft := w.clone()
	allowedItems := make(map[string]bool, len(draft.rules.Items))
	for _, it := range draft.rules.Items {
		allowedItems[it] = true
	}

	for _, ch := range changes {
		cs, ok := draft.chars[ch.Character]
		if !ok {
			return fmt.Errorf("world: 不存在的角色 %q", ch.Character)
		}
		if ch.MoveTo != "" {
			target, ok := draft.locs[ch.MoveTo]
			if !ok {
				return fmt.Errorf("world: 角色 %q 要前往不存在的地点 %q", ch.Character, ch.MoveTo)
			}
			if !draft.locs[cs.loc].edges[target.id] {
				return fmt.Errorf("world: 角色 %q 不能沿连通关系从 %q 前往 %q", ch.Character, cs.loc, ch.MoveTo)
			}
			cs.loc = target.id
		}
		for it, d := range ch.Items {
			if !allowedItems[it] {
				return fmt.Errorf("world: 角色 %q 的物品 %q 不在规则允许的物品种类中", ch.Character, it)
			}
			next := cs.items[it] + d
			if next < 0 {
				return fmt.Errorf("world: 角色 %q 的物品 %q 数量将为负", ch.Character, it)
			}
			total := 0
			for _, n := range cs.items {
				total += n
			}
			// 加上本次变化量（next 已在 map 中时用 next 替换旧值）
			total += d
			if total > draft.rules.CarryLimit {
				return fmt.Errorf("world: 角色 %q 携带物品总量将达 %d，超过上限 %d", ch.Character, total, draft.rules.CarryLimit)
			}
			cs.items[it] = next
		}
	}

	draft.time += advance
	*w = *draft
	return nil
}

// Seed 返回整数地图种子。
func (w *World) Seed() int { return w.seed }

// Time 返回当前时间片。
func (w *World) Time() int { return w.time }

// Rules 返回规则副本。
func (w *World) Rules() Rules {
	return Rules{
		Version:    w.rules.Version,
		Locations:  append([]string(nil), w.rules.Locations...),
		Items:      append([]string(nil), w.rules.Items...),
		CarryLimit: w.rules.CarryLimit,
	}
}

// Character 返回角色副本；角色不存在时 ok 为 false。
func (w *World) Character(id string) (Character, bool) {
	cs, ok := w.chars[id]
	if !ok {
		return Character{}, false
	}
	items := make(map[string]int, len(cs.items))
	for k, v := range cs.items {
		items[k] = v
	}
	return Character{
		ID:       cs.id,
		Location: cs.loc,
		Items:    items,
	}, true
}

// Location 返回地点副本；地点不存在时 ok 为 false。
func (w *World) Location(id string) (Location, bool) {
	ls, ok := w.locs[id]
	if !ok {
		return Location{}, false
	}
	conns := make([]string, 0, len(ls.edges))
	for e := range ls.edges {
		conns = append(conns, e)
	}
	sort.Strings(conns)
	return Location{ID: ls.id, Connections: conns}, true
}

// Snapshot 返回世界的完整深拷贝快照；调用方修改快照不影响世界。
func (w *World) Snapshot() WorldData {
	wd := WorldData{
		Seed: w.seed,
		Time: w.time,
		Rules: Rules{
			Version:    w.rules.Version,
			Locations:  append([]string(nil), w.rules.Locations...),
			Items:      append([]string(nil), w.rules.Items...),
			CarryLimit: w.rules.CarryLimit,
		},
	}

	locIDs := make([]string, 0, len(w.locs))
	for id := range w.locs {
		locIDs = append(locIDs, id)
	}
	sort.Strings(locIDs)
	for _, id := range locIDs {
		ls := w.locs[id]
		conns := make([]string, 0, len(ls.edges))
		for e := range ls.edges {
			conns = append(conns, e)
		}
		sort.Strings(conns)
		wd.Locations = append(wd.Locations, Location{ID: ls.id, Connections: conns})
	}

	charIDs := make([]string, 0, len(w.chars))
	for id := range w.chars {
		charIDs = append(charIDs, id)
	}
	sort.Strings(charIDs)
	for _, id := range charIDs {
		cs := w.chars[id]
		items := make(map[string]int, len(cs.items))
		for k, v := range cs.items {
			items[k] = v
		}
		wd.Characters = append(wd.Characters, Character{ID: cs.id, Location: cs.loc, Items: items})
	}

	return wd
}

func (w *World) clone() *World {
	cp := &World{
		seed:  w.seed,
		rules: w.rules,
		locs:  make(map[string]*locState, len(w.locs)),
		chars: make(map[string]*charState, len(w.chars)),
		time:  w.time,
	}
	for id, ls := range w.locs {
		e := &locState{id: ls.id, edges: make(map[string]bool, len(ls.edges))}
		for k, v := range ls.edges {
			e.edges[k] = v
		}
		cp.locs[id] = e
	}
	for id, cs := range w.chars {
		c := &charState{
			id:    cs.id,
			loc:   cs.loc,
			items: make(map[string]int, len(cs.items)),
		}
		for k, v := range cs.items {
			c.items[k] = v
		}
		cp.chars[id] = c
	}
	return cp
}
