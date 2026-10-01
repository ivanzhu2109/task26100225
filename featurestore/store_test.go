package featurestore

import (
	"math/rand"
	"sort"
	"testing"
)

// naive 是测试用的朴素参考实现：逐样本、逐事件按规则直接计算。
func naive(t *testing.T, s *Store, samples []Sample) []Result {
	t.Helper()
	results := make([]Result, len(samples))
	for i, smp := range samples {
		acc := s.accounts[smp.AccountID]
		if acc == nil {
			continue
		}
		for _, id := range acc.eventIDs {
			ev := s.events[id]
			ev.ensureSorted()
			// 选 VisibleTime <= K 的最新修订。
			j := sort.Search(len(ev.revs), func(j int) bool { return ev.revs[j].VisibleTime > smp.K }) - 1
			if j < 0 || ev.revs[j].Deleted {
				continue
			}
			r := ev.revs[j]
			if r.EventTime > smp.T-3600 && r.EventTime <= smp.T {
				results[i].Count++
				results[i].SumCents += r.AmountCents
			}
		}
	}
	return results
}

func checkEqual(t *testing.T, s *Store, samples []Sample) {
	t.Helper()
	got := s.Compute(samples)
	want := naive(t, s, samples)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d (%+v): got %+v, want %+v", i, samples[i], got[i], want[i])
		}
	}
}

// 晚到事件与回填更正：同一 T 不同 K 得到不同历史版本；VisibleTime == K 必须生效。
func TestLateAndBackfill(t *testing.T) {
	s := NewStore()
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 1000, VisibleTime: 500, AmountCents: 100},
	})
	// 晚到事件：可见时间晚于 K1，早于 K2。
	s.Import([]Revision{
		{EventID: 2, AccountID: 10, EventTime: 1000, VisibleTime: 1500, AmountCents: 200},
	})
	// 回填更正：事件 1 的金额在 VisibleTime=2000 被更正。
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 1000, VisibleTime: 2000, AmountCents: 300},
	})

	T := int64(2000)
	samples := []Sample{
		{AccountID: 10, T: T, K: 500},  // 只见事件1旧版
		{AccountID: 10, T: T, K: 1499}, // 事件2尚不可见
		{AccountID: 10, T: T, K: 1500}, // 事件2可见（K 边界生效）
		{AccountID: 10, T: T, K: 1999}, // 更正未生效
		{AccountID: 10, T: T, K: 2000}, // 更正生效（K 边界生效）
	}
	got := s.Compute(samples)
	want := []Result{
		{Count: 1, SumCents: 100},
		{Count: 1, SumCents: 100},
		{Count: 2, SumCents: 300},
		{Count: 2, SumCents: 300},
		{Count: 2, SumCents: 500},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// 修订把事件移入/移出窗口；移出后旧版本不得重新贡献。
func TestMoveInOutWindow(t *testing.T) {
	s := NewStore()
	T := int64(10000)
	s.Import([]Revision{
		// 初始在窗口内，后被移到窗口外。
		{EventID: 1, AccountID: 10, EventTime: 9900, VisibleTime: 100, AmountCents: 500},
		{EventID: 1, AccountID: 10, EventTime: 100, VisibleTime: 200, AmountCents: 500},
		// 初始在窗口外，后被移入窗口。
		{EventID: 2, AccountID: 10, EventTime: 100, VisibleTime: 100, AmountCents: 700},
		{EventID: 2, AccountID: 10, EventTime: 9990, VisibleTime: 300, AmountCents: 700},
	})
	samples := []Sample{
		{AccountID: 10, T: T, K: 100}, // 1 在内，2 在外
		{AccountID: 10, T: T, K: 200}, // 1 被移出：旧版本不得重新贡献
		{AccountID: 10, T: T, K: 300}, // 2 被移入
	}
	got := s.Compute(samples)
	want := []Result{
		{Count: 1, SumCents: 500},
		{Count: 0, SumCents: 0},
		{Count: 1, SumCents: 700},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// 删除后恢复；删除期间计数和金额都不得残留。
func TestDeleteAndRestore(t *testing.T) {
	s := NewStore()
	T := int64(10000)
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 9900, VisibleTime: 100, AmountCents: 500},
		{EventID: 1, AccountID: 10, VisibleTime: 200, Deleted: true},
		{EventID: 1, AccountID: 10, EventTime: 9950, VisibleTime: 300, AmountCents: 800},
	})
	samples := []Sample{
		{AccountID: 10, T: T, K: 100},
		{AccountID: 10, T: T, K: 200}, // 删除期间：无残留
		{AccountID: 10, T: T, K: 250}, // 仍在删除期间
		{AccountID: 10, T: T, K: 300}, // 恢复为新版本
	}
	got := s.Compute(samples)
	want := []Result{
		{Count: 1, SumCents: 500},
		{Count: 0, SumCents: 0},
		{Count: 0, SumCents: 0},
		{Count: 1, SumCents: 800},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// 窗口左右边界：(T-3600, T]，左开右闭。
func TestWindowBoundaries(t *testing.T) {
	s := NewStore()
	T := int64(10000)
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: T - 3600, VisibleTime: 1, AmountCents: 10}, // 左边界外（开区间）
		{EventID: 2, AccountID: 10, EventTime: T - 3599, VisibleTime: 1, AmountCents: 20}, // 左边界内
		{EventID: 3, AccountID: 10, EventTime: T, VisibleTime: 1, AmountCents: 40},        // 右边界内（闭区间）
		{EventID: 4, AccountID: 10, EventTime: T + 1, VisibleTime: 1, AmountCents: 80},    // 右边界外
	})
	got := s.Compute([]Sample{{AccountID: 10, T: T, K: 1}})
	want := Result{Count: 2, SumCents: 60}
	if got[0] != want {
		t.Fatalf("got %+v, want %+v", got[0], want)
	}
}

// 乱序、重复样本；单样本计算与批量计算结果一致。
func TestUnorderedDuplicateAndSingleConsistency(t *testing.T) {
	s := NewStore()
	rng := rand.New(rand.NewSource(42))
	var revs []Revision
	for e := int64(0); e < 50; e++ {
		account := e % 5
		visible := rng.Int63n(1000)
		for r := 0; r < 5; r++ {
			visible += 1 + rng.Int63n(100)
			revs = append(revs, Revision{
				EventID:     e,
				AccountID:   account,
				EventTime:   rng.Int63n(20000),
				VisibleTime: visible,
				AmountCents: rng.Int63n(20001) - 10000,
				Deleted:     rng.Intn(5) == 0,
			})
		}
	}
	// 打乱导入顺序（输入顺序无时间含义）。
	rng.Shuffle(len(revs), func(i, j int) { revs[i], revs[j] = revs[j], revs[i] })
	s.Import(revs[:len(revs)/2])
	s.Import(revs[len(revs)/2:])

	var samples []Sample
	for i := 0; i < 200; i++ {
		T := rng.Int63n(30000)
		K := T - rng.Int63n(T+1)
		samples = append(samples, Sample{AccountID: rng.Int63n(7), T: T, K: K})
	}
	// 加入重复样本。
	for i := 0; i < 50; i++ {
		samples = append(samples, samples[rng.Intn(len(samples))])
	}
	rng.Shuffle(len(samples), func(i, j int) { samples[i], samples[j] = samples[j], samples[i] })

	checkEqual(t, s, samples)

	// 单样本计算与批量计算完全一致。
	batch := s.Compute(samples)
	for i, smp := range samples {
		single := s.Compute([]Sample{smp})
		if single[0] != batch[i] {
			t.Fatalf("sample %d: single %+v != batch %+v", i, single[0], batch[i])
		}
	}
}

// 补入历史修订后，后续调用必须反映该修订在相应截点下的影响。
func TestImportAfterCompute(t *testing.T) {
	s := NewStore()
	T := int64(10000)
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 9900, VisibleTime: 100, AmountCents: 500},
	})
	before := s.Compute([]Sample{{AccountID: 10, T: T, K: 150}})
	if before[0] != (Result{Count: 1, SumCents: 500}) {
		t.Fatalf("before: got %+v", before[0])
	}
	// 补入一条 VisibleTime=120 的更正（在 K=150 截点下应生效）。
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 9900, VisibleTime: 120, AmountCents: 900},
	})
	after := s.Compute([]Sample{
		{AccountID: 10, T: T, K: 150},
		{AccountID: 10, T: T, K: 110}, // 该截点下仍是旧版本
	})
	want := []Result{{Count: 1, SumCents: 900}, {Count: 1, SumCents: 500}}
	for i := range want {
		if after[i] != want[i] {
			t.Fatalf("after[%d]: got %+v, want %+v", i, after[i], want[i])
		}
	}
}

// 随机对拍：与朴素参考实现比较。
func TestRandomizedAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	s := NewStore()
	for round := 0; round < 3; round++ {
		var revs []Revision
		for i := 0; i < 300; i++ {
			e := rng.Int63n(80)
			revs = append(revs, Revision{
				EventID:     e,
				AccountID:   100 + e%6,
				EventTime:   rng.Int63n(50000),
				VisibleTime: rng.Int63n(50000),
				AmountCents: rng.Int63n(2001) - 1000,
				Deleted:     rng.Intn(6) == 0,
			})
		}
		// 同一事件 VisibleTime 去重，保证互不相同。
		seen := make(map[[2]int64]bool)
		var dedup []Revision
		for _, r := range revs {
			key := [2]int64{r.EventID, r.VisibleTime}
			if !seen[key] {
				seen[key] = true
				dedup = append(dedup, r)
			}
		}
		s.Import(dedup)

		var samples []Sample
		for i := 0; i < 300; i++ {
			T := rng.Int63n(60000)
			K := T - rng.Int63n(T+1)
			samples = append(samples, Sample{AccountID: 100 + rng.Int63n(8), T: T, K: K})
		}
		checkEqual(t, s, samples)
	}
}
