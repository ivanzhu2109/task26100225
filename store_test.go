package featurestore

import (
	"math/rand"
	"testing"
)

// bruteForce 是朴素参考实现：逐样本、逐事件、逐修订扫描，严格按规则计算。
func bruteForce(s *Store, samples []Sample) []Result {
	out := make([]Result, len(samples))
	for i, smp := range samples {
		a := s.accounts[smp.AccountID]
		if a == nil {
			continue
		}
		var res Result
		for _, revs := range a.events {
			best := -1
			for j, r := range revs {
				if r.VisibleAt <= smp.K && (best < 0 || r.VisibleAt > revs[best].VisibleAt) {
					best = j
				}
			}
			if best < 0 || revs[best].Deleted {
				continue
			}
			et := revs[best].EventTime
			if et > smp.T-windowSeconds && et <= smp.T {
				res.Count++
				res.Sum += revs[best].Amount
			}
		}
		out[i] = res
	}
	return out
}

func checkAgainstBruteForce(t *testing.T, s *Store, samples []Sample) {
	t.Helper()
	got := s.Compute(samples)
	want := bruteForce(s, samples)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("样本 %d (%+v): 得到 %+v, 期望 %+v", i, samples[i], got[i], want[i])
		}
	}
}

// 晚到事件与回填更正：同一 T、不同 K 得到对应历史版本；VisibleAt == K 必须生效。
func TestLateArrivalAndBackfill(t *testing.T) {
	s := NewStore()
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 1000, VisibleAt: 500, Amount: 100},
	})
	// 晚到事件：VisibleAt 晚于早期截点。
	s.Import([]Revision{
		{EventID: 2, AccountID: 10, EventTime: 1000, VisibleAt: 900, Amount: 200},
	})
	// 回填更正：事件 1 的金额在 VisibleAt=1200 被更正。
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 1000, VisibleAt: 1200, Amount: 150},
	})

	const T = 2000
	samples := []Sample{
		{AccountID: 10, T: T, K: 500},  // 只见事件1旧版
		{AccountID: 10, T: T, K: 900},  // 事件1旧版 + 事件2
		{AccountID: 10, T: T, K: 1199}, // 更正尚未可见
		{AccountID: 10, T: T, K: 1200}, // 更正恰好可见，必须生效
	}
	got := s.Compute(samples)
	want := []Result{
		{Count: 1, Sum: 100},
		{Count: 2, Sum: 300},
		{Count: 2, Sum: 300},
		{Count: 2, Sum: 350},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("样本 %d: 得到 %+v, 期望 %+v", i, got[i], want[i])
		}
	}
}

// 修订改变业务发生时间，把事件移入/移出窗口；移出后旧版本不得重新贡献。
func TestRevisionMovesEventInAndOutOfWindow(t *testing.T) {
	s := NewStore()
	const T = 10000
	s.Import([]Revision{
		// 初始版本在窗口内。
		{EventID: 1, AccountID: 10, EventTime: T - 100, VisibleAt: 100, Amount: 500},
		// 更正把业务时间移到窗口外。
		{EventID: 1, AccountID: 10, EventTime: T - 7200, VisibleAt: 200, Amount: 500},
		// 再次更正移回窗口内，且金额变化。
		{EventID: 1, AccountID: 10, EventTime: T - 50, VisibleAt: 300, Amount: 700},
	})
	samples := []Sample{
		{AccountID: 10, T: T, K: 100},
		{AccountID: 10, T: T, K: 200}, // 移出窗口：计数金额都必须为 0
		{AccountID: 10, T: T, K: 300}, // 移回窗口，使用新金额
	}
	got := s.Compute(samples)
	want := []Result{
		{Count: 1, Sum: 500},
		{Count: 0, Sum: 0},
		{Count: 1, Sum: 700},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("样本 %d: 得到 %+v, 期望 %+v", i, got[i], want[i])
		}
	}
}

// 删除后恢复；删除期间计数和金额都不得残留。
func TestDeleteAndRestore(t *testing.T) {
	s := NewStore()
	const T = 5000
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: T - 10, VisibleAt: 100, Amount: 300},
		{EventID: 1, AccountID: 10, VisibleAt: 200, Deleted: true},
		{EventID: 1, AccountID: 10, EventTime: T - 20, VisibleAt: 300, Amount: 400},
	})
	samples := []Sample{
		{AccountID: 10, T: T, K: 100},
		{AccountID: 10, T: T, K: 200}, // 删除期：无残留
		{AccountID: 10, T: T, K: 250}, // 仍在删除期
		{AccountID: 10, T: T, K: 300}, // 恢复，使用恢复版本
	}
	got := s.Compute(samples)
	want := []Result{
		{Count: 1, Sum: 300},
		{Count: 0, Sum: 0},
		{Count: 0, Sum: 0},
		{Count: 1, Sum: 400},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("样本 %d: 得到 %+v, 期望 %+v", i, got[i], want[i])
		}
	}
}

// 窗口左右边界：EventTime == T 计入；EventTime == T-3600 不计入。
func TestWindowBoundaries(t *testing.T) {
	s := NewStore()
	const T = 100000
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: T, VisibleAt: 1, Amount: 1},            // 右边界：计入
		{EventID: 2, AccountID: 10, EventTime: T - 3600, VisibleAt: 1, Amount: 10},    // 左边界：不计入
		{EventID: 3, AccountID: 10, EventTime: T - 3599, VisibleAt: 1, Amount: 100},   // 左边界内一秒：计入
		{EventID: 4, AccountID: 10, EventTime: T + 1, VisibleAt: 1, Amount: 1000},     // 未来：不计入
		{EventID: 5, AccountID: 10, EventTime: T - 3601, VisibleAt: 1, Amount: 10000}, // 窗口外：不计入
	})
	got := s.Compute([]Sample{{AccountID: 10, T: T, K: 1}})
	want := Result{Count: 2, Sum: 101}
	if got[0] != want {
		t.Fatalf("得到 %+v, 期望 %+v", got[0], want)
	}
}

// 空窗口、未知账户、无任何可见修订。
func TestEmptyCases(t *testing.T) {
	s := NewStore()
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 100, VisibleAt: 500, Amount: 100},
	})
	got := s.Compute([]Sample{
		{AccountID: 99, T: 1000, K: 1000}, // 未知账户
		{AccountID: 10, T: 1000, K: 400},  // 截点前无任何修订
		{AccountID: 10, T: 5000, K: 500},  // 窗口内无事件
	})
	for i, r := range got {
		if r != (Result{}) {
			t.Fatalf("样本 %d: 得到 %+v, 期望零值", i, r)
		}
	}
}

// 乱序、重复样本；单条计算与批量计算结果完全一致。
func TestShuffledDuplicateSamplesAndSingleVsBatch(t *testing.T) {
	s := NewStore()
	rng := rand.New(rand.NewSource(42))
	var revs []Revision
	for e := int64(0); e < 200; e++ {
		acc := int64(e % 5)
		visible := rng.Int63n(1000)
		for v := 0; v < 1+rng.Intn(4); v++ {
			r := Revision{
				EventID:   e,
				AccountID: acc,
				EventTime: rng.Int63n(20000),
				VisibleAt: visible,
				Amount:    rng.Int63n(2000) - 1000,
				Deleted:   rng.Intn(6) == 0,
			}
			visible += 1 + rng.Int63n(1000)
			revs = append(revs, r)
		}
	}
	// 乱序导入。
	rng.Shuffle(len(revs), func(i, j int) { revs[i], revs[j] = revs[j], revs[i] })
	s.Import(revs[:len(revs)/2])
	s.Import(revs[len(revs)/2:])

	// 乱序、重复样本。
	var samples []Sample
	for i := 0; i < 500; i++ {
		T := rng.Int63n(30000)
		K := rng.Int63n(T + 1)
		samples = append(samples, Sample{AccountID: int64(rng.Intn(7)), T: T, K: K})
	}
	for i := 0; i < 100; i++ {
		samples = append(samples, samples[rng.Intn(len(samples))])
	}

	batch := s.Compute(samples)
	checkAgainstBruteForce(t, s, samples)

	// 逐条单样本计算，结果必须与批量一致。
	for i, smp := range samples {
		single := s.Compute([]Sample{smp})
		if single[0] != batch[i] {
			t.Fatalf("样本 %d: 单条 %+v != 批量 %+v", i, single[0], batch[i])
		}
	}
}

// 补入历史修订后，后续调用必须反映该修订在相应截点下的影响。
func TestImportAffectsSubsequentCompute(t *testing.T) {
	s := NewStore()
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 900, VisibleAt: 100, Amount: 100},
	})
	smp := Sample{AccountID: 10, T: 1000, K: 500}
	if got := s.Compute([]Sample{smp})[0]; got != (Result{Count: 1, Sum: 100}) {
		t.Fatalf("首次计算: %+v", got)
	}
	// 截点早于任何已知修订：暂无可见版本。
	early := Sample{AccountID: 10, T: 1000, K: 75}
	if got := s.Compute([]Sample{early})[0]; got != (Result{}) {
		t.Fatalf("补入前早截点: %+v", got)
	}
	// 补入一条更早可见的更正（输入顺序无时间含义）。
	s.Import([]Revision{
		{EventID: 1, AccountID: 10, EventTime: 900, VisibleAt: 50, Amount: 999},
	})
	// 早截点现在可见补入的版本。
	if got := s.Compute([]Sample{early})[0]; got != (Result{Count: 1, Sum: 999}) {
		t.Fatalf("补入后早截点: %+v", got)
	}
	// 原截点仍以 VisibleAt=100 的更新版本为准。
	if got := s.Compute([]Sample{smp})[0]; got != (Result{Count: 1, Sum: 100}) {
		t.Fatalf("补入后计算: %+v", got)
	}
}
