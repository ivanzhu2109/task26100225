# featurestore

账户时点特征库（Go），由训练和推理代码直接调用，无接口层；事件历史保存在进程内内存。

## 用法

```go
s := featurestore.NewStore()
s.Import(revisions)              // 可多次导入，输入顺序无时间含义
results := s.Compute(samples)    // 批量计算；单样本 = 只有一条样本的批量
```

- `Revision`：逻辑事件的一条完整修订（EventID / AccountID / EventTime / VisibleTime / AmountCents / Deleted）。
- `Sample`：账户、预测时刻 T、可见截点 K（K ≤ T），整数秒。
- `Result`：窗口 (T-3600, T] 内可见事件数与金额总和（分）。

## 可见性规则

1. 对每个事件，选 VisibleTime ≤ K 的最新修订；没有或是删除修订则事件不存在。
2. 选定版本后，仅保留 EventTime ∈ (T-3600, T] 的事件；不会因最新修订在窗口外而回退旧版本。
3. 每个逻辑事件最多贡献一次；空窗口计数与金额均为 0。

## 实现

按账户分组样本，组内按 K 升序增量应用修订（每条修订全程只应用一次），
用 Fenwick 树（坐标压缩 EventTime）维护当前可见事件的计数与金额和，
每条样本一次范围查询。复杂度约为 O((修订数 + 样本数) log 事件时间种数)。

## 测试

```sh
go test ./...
```

- `featurestore/store_test.go`：晚到事件与回填更正（同 T 不同 K、VisibleTime == K 生效）、
  修订移入/移出窗口、删除后恢复、窗口左右边界、乱序/重复样本、
  单样本与批量一致性、多次导入后可见性，以及随机对拍朴素参考实现。
- `featurestore/scale_test.go`：20 万事件 / 60 万修订 / 10 万样本规模验收
  （90% 事件集中在一个账户、截点大量不同且乱序、修订反复改变窗口归属），
  记录耗时与峰值堆内存，断言合计 < 15s、峰值 < 512 MiB。
  本机实测：导入 ~0.17s，计算 ~0.49s，峰值堆内存 ~152 MiB。
