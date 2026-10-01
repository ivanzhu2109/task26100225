package featurestore

import "sort"

const windowSeconds = 3600

// fenwick 树状数组，支持下标点更新与前缀和查询。
type fenwick struct {
	n    int
	tree []int64
}

func newFenwick(n int) *fenwick {
	return &fenwick{n: n, tree: make([]int64, n+1)}
}

func (f *fenwick) add(i int, delta int64) {
	for i++; i <= f.n; i += i & -i {
		f.tree[i] += delta
	}
}

// sum 返回 [0, i) 的前缀和。
func (f *fenwick) sum(i int) int64 {
	var s int64
	for ; i > 0; i -= i & -i {
		s += f.tree[i]
	}
	return s
}

// Compute 按样本批量计算特征，结果与样本逐条对应。
// 只使用本次调用前已导入的历史。
//
// 算法：按账户分组样本；账户内按 K 升序扫描，修订按 VisibleAt 顺序逐步生效，
// 每个事件始终只保留“VisibleAt <= K 的最新修订”作为当前版本；
// 当前版本以 (EventTime, Amount) 的形式加入/移出树状数组，
// 每条样本查询 EventTime ∈ (T-3600, T] 的计数与金额和。
func (s *Store) Compute(samples []Sample) []Result {
	results := make([]Result, len(samples))

	// 按账户分组样本下标。
	byAccount := make(map[int64][]int, len(samples))
	for i, smp := range samples {
		byAccount[smp.AccountID] = append(byAccount[smp.AccountID], i)
	}

	for accID, idxs := range byAccount {
		a := s.accounts[accID]
		if a == nil {
			continue // 无历史：结果为零值
		}
		s.computeAccount(a, samples, idxs, results)
	}
	return results
}

func (s *Store) computeAccount(a *account, samples []Sample, idxs []int, results []Result) {
	// 确保每个事件的修订按 VisibleAt 升序。
	if !a.sorted {
		for _, revs := range a.events {
			sort.Slice(revs, func(i, j int) bool { return revs[i].VisibleAt < revs[j].VisibleAt })
		}
		a.sorted = true
	}

	// 样本按 K 升序。
	sort.Slice(idxs, func(i, j int) bool { return samples[idxs[i]].K < samples[idxs[j]].K })

	// 坐标压缩：所有修订的 EventTime。
	times := make([]int64, 0, 1024)
	flat := make([]Revision, 0, 1024) // 全部修订，按 VisibleAt 排序后逐步生效
	for _, revs := range a.events {
		for _, r := range revs {
			times = append(times, r.EventTime)
			flat = append(flat, r)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	times = uniqueInt64(times)
	sort.Slice(flat, func(i, j int) bool {
		if flat[i].VisibleAt != flat[j].VisibleAt {
			return flat[i].VisibleAt < flat[j].VisibleAt
		}
		return flat[i].EventID < flat[j].EventID
	})

	bitCount := newFenwick(len(times))
	bitSum := newFenwick(len(times))

	// 每个事件的当前生效版本（curIdx 指向 a.events[eventID] 中的位置，-1 表示无）。
	curIdx := make(map[int64]int, len(a.events))
	for id := range a.events {
		curIdx[id] = -1
	}

	apply := func(r Revision, sign int64) {
		// 将 r 的版本贡献加入（sign=+1）或移出（sign=-1）树状数组。
		pos := sort.Search(len(times), func(i int) bool { return times[i] >= r.EventTime })
		bitCount.add(pos, sign)
		bitSum.add(pos, sign*r.Amount)
	}

	next := 0 // flat 中下一条待生效的修订
	for _, si := range idxs {
		smp := samples[si]
		// 生效所有 VisibleAt <= K 的修订。
		for next < len(flat) && flat[next].VisibleAt <= smp.K {
			r := flat[next]
			next++
			revs := a.events[r.EventID]
			old := curIdx[r.EventID]
			if old >= 0 && !revs[old].Deleted {
				apply(revs[old], -1)
			}
			// 二分找到该修订在事件修订列表中的位置（VisibleAt 唯一）。
			ni := sort.Search(len(revs), func(i int) bool { return revs[i].VisibleAt >= r.VisibleAt })
			curIdx[r.EventID] = ni
			if !r.Deleted {
				apply(r, +1)
			}
		}
		// 查询 EventTime ∈ (T-3600, T]。
		hi := sort.Search(len(times), func(i int) bool { return times[i] > smp.T })
		lo := sort.Search(len(times), func(i int) bool { return times[i] > smp.T-windowSeconds })
		results[si] = Result{
			Count: int(bitCount.sum(hi) - bitCount.sum(lo)),
			Sum:   bitSum.sum(hi) - bitSum.sum(lo),
		}
	}
}

func uniqueInt64(xs []int64) []int64 {
	if len(xs) == 0 {
		return xs
	}
	out := xs[:1]
	for _, x := range xs[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}
