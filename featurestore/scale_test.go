package featurestore

import (
	"math/rand"
	"runtime"
	"testing"
	"time"
)

// 规模验收：20 万事件、60 万修订、10 万样本。
// 90% 事件集中在一个账户；样本截点大量不同且乱序；修订反复改变窗口归属。
// 从导入历史到全部结果可用合计不超过 15 秒，峰值堆内存不超过 512 MiB。
func TestScale(t *testing.T) {
	const (
		numEvents    = 200_000
		numRevisions = 600_000
		numSamples   = 100_000
		hotAccount   = int64(1)
	)
	start := time.Now()

	// 后台采样峰值堆内存。
	var peak uint64
	done := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-done:
				return
			default:
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak {
					peak = ms.HeapInuse
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	defer close(done)

	rng := rand.New(rand.NewSource(1))
	baseT := int64(1_000_000)

	// 生成修订：每个事件 3 条，EventTime 围绕 baseT 附近波动，
	// 使修订反复把事件移入/移出样本窗口。
	revs := make([]Revision, 0, numRevisions)
	for e := int64(0); e < numEvents; e++ {
		account := hotAccount
		if e%10 == 0 { // 10% 分散到其它账户
			account = 1000 + e%100
		}
		visible := rng.Int63n(5000)
		for r := 0; r < numRevisions/numEvents; r++ {
			visible += 1 + rng.Int63n(1000)
			revs = append(revs, Revision{
				EventID:     e,
				AccountID:   account,
				EventTime:   baseT - 7200 + rng.Int63n(14400), // 窗口附近 ±2h
				VisibleTime: visible,
				AmountCents: rng.Int63n(200001) - 100000,
				Deleted:     rng.Intn(8) == 0,
			})
		}
	}
	// 打乱导入顺序。
	rng.Shuffle(len(revs), func(i, j int) { revs[i], revs[j] = revs[j], revs[i] })

	s := NewStore()
	// 分三批导入，模拟多次导入。
	third := len(revs) / 3
	s.Import(revs[:third])
	s.Import(revs[third : 2*third])
	s.Import(revs[2*third:])
	importElapsed := time.Since(start)

	// 样本：截点大量不同且乱序，含重复样本。
	samples := make([]Sample, 0, numSamples)
	for i := 0; i < numSamples-1000; i++ {
		T := baseT + rng.Int63n(3600)
		K := T - rng.Int63n(8000)
		account := hotAccount
		if i%10 == 0 {
			account = 1000 + rng.Int63n(100)
		}
		samples = append(samples, Sample{AccountID: account, T: T, K: K})
	}
	for i := 0; i < 1000; i++ { // 重复样本
		samples = append(samples, samples[rng.Intn(len(samples))])
	}
	rng.Shuffle(len(samples), func(i, j int) { samples[i], samples[j] = samples[j], samples[i] })

	computeStart := time.Now()
	results := s.Compute(samples)
	computeElapsed := time.Since(computeStart)
	totalElapsed := time.Since(start)

	// 正确性抽查：随机 100 条样本与朴素实现对比；另抽 5 条验证单样本一致。
	checkIdx := rng.Perm(len(samples))[:100]
	sub := make([]Sample, 0, 200)
	for _, i := range checkIdx {
		sub = append(sub, samples[i])
	}
	want := naive(t, s, sub)
	for j, i := range checkIdx {
		if results[i] != want[j] {
			t.Fatalf("sample %d (%+v): got %+v, want %+v", i, samples[i], results[i], want[j])
		}
	}
	for _, i := range checkIdx[:5] {
		single := s.Compute([]Sample{samples[i]})
		if single[0] != results[i] {
			t.Fatalf("sample %d: single %+v != batch %+v", i, single[0], results[i])
		}
	}

	t.Logf("import: %v, compute: %v, total: %v", importElapsed, computeElapsed, totalElapsed)
	t.Logf("peak heap in-use: %.1f MiB", float64(peak)/(1<<20))
	if totalElapsed > 15*time.Second {
		t.Fatalf("too slow: %v > 15s", totalElapsed)
	}
	if peak > 512*(1<<20) {
		t.Fatalf("peak memory %.1f MiB exceeds 512 MiB", float64(peak)/(1<<20))
	}
}
