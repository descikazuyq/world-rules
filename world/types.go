package world

// Rules 是建立世界时必须满足的规则集合。
//
// Version 是非空的规则版本；Locations 是允许出现的地点；Edges 描述地点
// 之间的连通关系（无向，移动可以沿任一方向发生）；ItemKinds 是允许出现
// 的物品种类；CarryLimits 给出每个角色可携带物品的总量上限。
//
// 全部文本标识（版本、地点、道路两端、物品种类、携带上限的角色键）都必须
// 是合法 UTF-8：含无效字节的标识会被拒绝，因为保存为 JSON 时它们会被改写
// 成替换字符，可能让原本不同的标识在存档中重名。合法 UTF-8 中真实存在的
// “�”、中文、表情符号等按普通标识接受；名称比较是精确相等，不做大小写
// 转换、去空格或 Unicode 归一化。
type Rules struct {
	Version     string
	Locations   []string
	Edges       []Edge
	ItemKinds   []string
	CarryLimits map[string]int
}

// Edge 表示两个地点之间的一条连通关系。
type Edge struct {
	From string
	To   string
}

// CharacterItem 是角色携带的一种物品及其数量。
type CharacterItem struct {
	Item  string
	Count int
}

// Character 是一个角色：标识、所在地点和携带物品。
//
// 标识、所在地点与物品名称都必须是合法 UTF-8（见 Rules 的说明）。
type Character struct {
	ID       string
	Location string
	Items    []CharacterItem
}

// State 是某一时刻世界的完整状态。
//
// Time 是时间片，世界建立时从零开始，只能向前推进。
type State struct {
	Seed       int64
	Rules      Rules
	Time       int
	Characters []Character
}

// InitialData 是建立世界所需的初始数据。
type InitialData struct {
	// Seed 是整数地图种子。
	Seed int64
	// Rules 是非空规则。
	Rules Rules
	// Characters 是初始角色集合，角色必须位于规则允许的地点，
	// 携带的物品必须合法且不超过携带上限。
	Characters []Character
}

// Move 要求角色沿一条允许的连通关系移动到另一地点。
type Move struct {
	Character string
	To        string
}

// ItemChange 表示某角色持有的某种物品数量变化（Delta 可正可负）。
type ItemChange struct {
	Character string
	Item      string
	Delta     int
}

// Commit 是一次原子提交：若干移动、物品数量变化和一次时间推进。
//
// Time 是提交后期望到达的绝对时间片，不能早于当前时间片。
// 任意一项不合法都会导致整次提交失败，世界保持提交前状态。
type Commit struct {
	Moves       []Move
	ItemChanges []ItemChange
	Time        int
}

func cloneRules(r Rules) Rules {
	cp := Rules{
		Version:   r.Version,
		Locations: append([]string(nil), r.Locations...),
		Edges:     append([]Edge(nil), r.Edges...),
		ItemKinds: append([]string(nil), r.ItemKinds...),
	}
	if r.CarryLimits != nil {
		cp.CarryLimits = make(map[string]int, len(r.CarryLimits))
		for k, v := range r.CarryLimits {
			cp.CarryLimits[k] = v
		}
	}
	return cp
}

func cloneCharacters(chars []Character) []Character {
	if chars == nil {
		return nil
	}
	cp := make([]Character, len(chars))
	for i, c := range chars {
		cp[i] = Character{
			ID:       c.ID,
			Location: c.Location,
			Items:    append([]CharacterItem(nil), c.Items...),
		}
	}
	return cp
}

func cloneState(s State) State {
	return State{
		Seed:       s.Seed,
		Rules:      cloneRules(s.Rules),
		Time:       s.Time,
		Characters: cloneCharacters(s.Characters),
	}
}
