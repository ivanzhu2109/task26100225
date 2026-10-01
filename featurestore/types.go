// Package featurestore 提供账户时点特征库：根据事件修订历史，
// 按样本的预测时刻 T 与数据可见截点 K 还原当时可见的账户特征。
package featurestore

// Revision 是某个逻辑事件的一条修订（完整版本，非增量）。
type Revision struct {
	EventID     int64 // 逻辑事件唯一编号
	AccountID   int64 // 所属账户，同一事件固定
	EventTime   int64 // 业务发生时间，整数秒
	VisibleTime int64 // 上游可见时间，整数秒；同一事件各修订互不相同
	AmountCents int64 // 以分计的有符号金额
	Deleted     bool  // 删除修订：该事件在此版本暂时不存在
}

// Sample 描述一条取数样本。
type Sample struct {
	AccountID int64 // 账户
	T         int64 // 预测时刻，整数秒
	K         int64 // 数据可见截点，整数秒，K <= T
}

// Result 是一条样本对应的特征结果。
type Result struct {
	Count    int64 // 窗口内可见事件数
	SumCents int64 // 窗口内可见事件金额总和（分）
}
