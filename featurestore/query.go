package featurestore

import "sort"

// fenwick 树同时维护事件计数与金额和，坐标为压缩后的 EventTime。
type fenwick struct {
	n     int
	count []int64
	sum   []int64
}

func newFenwick(n int) *fenwick {
	return &fenwick{n: n, count: make([]int64, n+1), sum: make([]int64, n+1)}
}

// add 在位置 i（0 基）上累加计数 delta 与金额 amount。
// 删除时点应传 delta=-1 与旧金额的相反数。
func (f *fenwick) add(i int, delta, amount int64) {
	for x := i + 1; x <= f.n; x += x & (-x) {
		f.count[x] += delta
		f.sum[x] += amount
	}
}

// prefix 返回前 i 个坐标（[0, i)）的计数与金额和。
func (f *fenwick) prefix(i int) (int64, int64) {
	var c, s int64
	for x := i; x > 0; x -= x & (-x) {
		c += f.count[x]
		s += f.sum[x]
	}
	return c, s
}

// revRef 指向某事件的一条修订，用于按 VisibleTime 全局排序后增量应用。
type revRef struct {
	visibleTime int64
	eventIdx    int // 在账户事件切片中的下标
	revIdx      int // 在事件修订切片中的下标
}

// Compute 按样本批量计算特征，结果与样本逐条对应。
// 只使用调用前已导入的历史；单样本计算是批量计算的特例。
func (s *Store) Compute(samples []Sample) []Result {
	results := make([]Result, len(samples))

	// 按账户分组样本下标，不同账户互不影响。
	byAccount := make(map[int64][]int)
	for i, smp := range samples {
		byAccount[smp.AccountID] = append(byAccount[smp.AccountID], i)
	}

	for accountID, idxs := range byAccount {
		acc := s.accounts[accountID]
		if acc == nil {
			continue // 无历史：结果为零值
		}
		s.computeAccount(acc, samples, idxs, results)
	}
	return results
}

// computeAccount 处理同一账户的全部样本：
// 将样本按 K 升序排列，增量应用 VisibleTime <= K 的修订，
// 用 Fenwick 树维护当前可见事件的 (EventTime, Amount) 点集，
// 再按窗口 (T-3600, T] 做范围查询。
func (s *Store) computeAccount(acc *accountState, samples []Sample, idxs []int, results []Result) {
	events := acc.eventIDs

	// 坐标压缩：该账户所有修订出现过的 EventTime。
	coords := make([]int64, 0, len(events))
	refs := make([]revRef, 0, len(events))
	for i, id := range events {
		ev := s.events[id]
		ev.ensureSorted()
		for j, r := range ev.revs {
			coords = append(coords, r.EventTime)
			refs = append(refs, revRef{visibleTime: r.VisibleTime, eventIdx: i, revIdx: j})
		}
	}
	sort.Slice(coords, func(a, b int) bool { return coords[a] < coords[b] })
	uniq := coords[:0]
	for _, v := range coords {
		if len(uniq) == 0 || uniq[len(uniq)-1] != v {
			uniq = append(uniq, v)
		}
	}
	coords = uniq
	sort.Slice(refs, func(a, b int) bool { return refs[a].visibleTime < refs[b].visibleTime })

	fw := newFenwick(len(coords))
	curPos := make([]int, len(events))   // 当前点在 coords 中的位置，-1 表示事件当前不存在
	curAmt := make([]int64, len(events)) // 当前点的金额
	for i := range curPos {
		curPos[i] = -1
	}

	// 样本按 K 升序，K 相同时顺序无关（应用同一批修订）。
	sorted := make([]int, len(idxs))
	copy(sorted, idxs)
	sort.Slice(sorted, func(a, b int) bool { return samples[sorted[a]].K < samples[sorted[b]].K })

	applied := 0 // refs 中已应用的位置
	for _, si := range sorted {
		smp := samples[si]
		for applied < len(refs) && refs[applied].visibleTime <= smp.K {
			ref := refs[applied]
			applied++
			ev := s.events[events[ref.eventIdx]]
			r := ev.revs[ref.revIdx]
			if curPos[ref.eventIdx] >= 0 {
				fw.add(curPos[ref.eventIdx], -1, -curAmt[ref.eventIdx])
				curPos[ref.eventIdx] = -1
			}
			if !r.Deleted {
				pos := sort.Search(len(coords), func(i int) bool { return coords[i] >= r.EventTime })
				fw.add(pos, 1, r.AmountCents)
				curPos[ref.eventIdx] = pos
				curAmt[ref.eventIdx] = r.AmountCents
			}
		}
		// 窗口 (T-3600, T]：坐标下标 (lower, upper]。
		lower := sort.Search(len(coords), func(i int) bool { return coords[i] > smp.T-3600 })
		upper := sort.Search(len(coords), func(i int) bool { return coords[i] > smp.T })
		c2, s2 := fw.prefix(upper)
		c1, s1 := fw.prefix(lower)
		results[si] = Result{Count: c2 - c1, SumCents: s2 - s1}
	}
}
