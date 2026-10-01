package featurestore

import (
	"math/rand"
	"runtime"
	"syscall"
	"testing"
	"time"
)

const (
	scaleEvents    = 200_000
	scaleRevisions = 600_000
	scaleSamples   = 100_000
	maxElapsed     = 15 * time.Second
	maxPeakMem     = 512 << 20 // 512 MiB
)

// peakRSS 返回进程峰值常驻内存（字节）。
func peakRSS() uint64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	// macOS 下 Maxrss 单位为字节；Linux 下为 KB。
	if runtime.GOOS == "darwin" {
		return uint64(ru.Maxrss)
	}
	return uint64(ru.Maxrss) << 10
}

// TestScaleAcceptance 规模验收：20万事件 / 60万修订 / 10万样本。
// 90% 事件集中在一个账户；样本截点大量不同且乱序；修订反复改变窗口归属。
func TestScaleAcceptance(t *testing.T) {
	rng := rand.New(rand.NewSource(7))

	const hotAccount = int64(1)
	const otherAccounts = 1000

	// 生成修订：每个事件 3 条，业务时间在窗口内外反复跳动。
	revs := make([]Revision, 0, scaleRevisions)
	perEvent := scaleRevisions / scaleEvents
	base := int64(1_700_000_000)
	for e := int64(0); e < scaleEvents; e++ {
		acc := hotAccount
		if e%10 == 0 { // 10% 分散到其他账户
			acc = int64(2 + e%otherAccounts)
		}
		center := base + e%5000 // 让事件围绕各自窗口中心
		visible := base - 100000 + e%1000
		for v := 0; v < perEvent; v++ {
			// 交替把事件放在窗口内 / 窗口外，反复改变窗口归属。
			offset := int64(1800)
			if v%2 == 1 {
				offset = int64(7200)
			}
			revs = append(revs, Revision{
				EventID:   e,
				AccountID: acc,
				EventTime: center - offset + int64(v),
				VisibleAt: visible,
				Amount:    int64(v+1)*100 + e%50,
				Deleted:   v == perEvent-1 && e%17 == 0, // 少量删除修订
			})
			visible += int64(1 + e%97)
		}
	}
	// 乱序导入（输入顺序无时间含义）。
	rng.Shuffle(len(revs), func(i, j int) { revs[i], revs[j] = revs[j], revs[i] })

	// 生成样本：截点大量不同、乱序，含重复。
	samples := make([]Sample, 0, scaleSamples)
	for i := 0; i < scaleSamples; i++ {
		acc := hotAccount
		if i%10 == 0 {
			acc = int64(2 + rng.Intn(otherAccounts))
		}
		T := base + rng.Int63n(6000)
		K := T - rng.Int63n(200000)
		samples = append(samples, Sample{AccountID: acc, T: T, K: K})
	}
	for i := 0; i < 1000; i++ { // 重复样本
		samples = append(samples, samples[rng.Intn(len(samples))])
	}
	rng.Shuffle(len(samples), func(i, j int) { samples[i], samples[j] = samples[j], samples[i] })

	// 计时：从导入历史开始，到全部样本结果可用。
	start := time.Now()
	s := NewStore()
	const chunk = 100_000 // 分多批导入，模拟多次导入
	for i := 0; i < len(revs); i += chunk {
		end := i + chunk
		if end > len(revs) {
			end = len(revs)
		}
		s.Import(revs[i:end])
	}
	results := s.Compute(samples)
	elapsed := time.Since(start)

	if len(results) != len(samples) {
		t.Fatalf("结果数 %d != 样本数 %d", len(results), len(samples))
	}

	// 正确性抽查：随机样本对照朴素参考实现。
	checkIdx := rng.Perm(len(samples))[:100]
	sub := make([]Sample, len(checkIdx))
	for i, idx := range checkIdx {
		sub[i] = samples[idx]
	}
	want := bruteForce(s, sub)
	for i, idx := range checkIdx {
		if results[idx] != want[i] {
			t.Fatalf("样本 %d (%+v): 批量 %+v != 参考 %+v", idx, samples[idx], results[idx], want[i])
		}
	}

	// 单样本计算与批量计算完全一致（抽查）。
	for _, idx := range checkIdx[:50] {
		single := s.Compute([]Sample{samples[idx]})
		if single[0] != results[idx] {
			t.Fatalf("样本 %d: 单条 %+v != 批量 %+v", idx, single[0], results[idx])
		}
	}

	peak := peakRSS()
	t.Logf("规模验收: 事件=%d 修订=%d 样本=%d 耗时=%v 峰值内存=%.1f MiB",
		scaleEvents, len(revs), len(samples), elapsed, float64(peak)/float64(1<<20))
	if elapsed > maxElapsed {
		t.Fatalf("耗时 %v 超过上限 %v", elapsed, maxElapsed)
	}
	if peak > maxPeakMem {
		t.Fatalf("峰值内存 %d 字节超过上限 %d 字节", peak, maxPeakMem)
	}
}
