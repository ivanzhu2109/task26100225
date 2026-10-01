package featurestore

// Store 是账户时点特征库，事件历史保存在进程内内存。
// 仅支持单进程顺序调用，不保证并发安全。
type Store struct {
	accounts map[int64]*account
}

// account 保存一个账户下所有事件的修订历史。
type account struct {
	events map[int64][]Revision // eventID -> 修订列表（导入顺序，未排序）
	sorted bool                 // 修订列表是否已按 VisibleAt 排序
}

// NewStore 创建一个空的特征库。
func NewStore() *Store {
	return &Store{accounts: make(map[int64]*account)}
}

// Import 导入一批事件修订历史。可多次调用，输入顺序没有时间含义。
func (s *Store) Import(revs []Revision) {
	for _, r := range revs {
		a := s.accounts[r.AccountID]
		if a == nil {
			a = &account{events: make(map[int64][]Revision)}
			s.accounts[r.AccountID] = a
		}
		a.events[r.EventID] = append(a.events[r.EventID], r)
		a.sorted = false
	}
}
