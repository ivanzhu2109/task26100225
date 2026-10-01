package featurestore

import "sort"

// eventState 保存一个逻辑事件的全部修订，按 VisibleTime 升序。
type eventState struct {
	accountID int64
	revs      []Revision // 按 VisibleTime 升序（sorted 为 true 时保证有序）
	sorted    bool
}

// accountState 保存一个账户下的事件集合。
type accountState struct {
	eventIDs []int64
}

// Store 是进程内内存的账户时点特征库。
type Store struct {
	events   map[int64]*eventState
	accounts map[int64]*accountState
}

// NewStore 创建空的特征库。
func NewStore() *Store {
	return &Store{
		events:   make(map[int64]*eventState),
		accounts: make(map[int64]*accountState),
	}
}

// Import 导入一批事件修订历史。可多次调用，输入顺序没有时间含义。
func (s *Store) Import(revisions []Revision) {
	for _, r := range revisions {
		ev, ok := s.events[r.EventID]
		if !ok {
			ev = &eventState{accountID: r.AccountID}
			s.events[r.EventID] = ev
			acc := s.accounts[r.AccountID]
			if acc == nil {
				acc = &accountState{}
				s.accounts[r.AccountID] = acc
			}
			acc.eventIDs = append(acc.eventIDs, r.EventID)
		}
		ev.revs = append(ev.revs, r)
		ev.sorted = false
	}
}

// ensureSorted 保证事件的修订按 VisibleTime 升序。
func (e *eventState) ensureSorted() {
	if e.sorted {
		return
	}
	sort.Slice(e.revs, func(i, j int) bool {
		return e.revs[i].VisibleTime < e.revs[j].VisibleTime
	})
	e.sorted = true
}
