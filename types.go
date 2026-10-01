package featurestore

// Revision 是某逻辑事件的一条修订。每条有效修订都是完整版本。
type Revision struct {
	EventID   int64 // 逻辑事件唯一编号
	AccountID int64 // 所属账户，同一事件固定
	EventTime int64 // 业务发生时间，整数秒
	VisibleAt int64 // 上游可见时间，整数秒；同一事件各修订互不相同
	Amount    int64 // 金额，以分计的有符号整数
	Deleted   bool  // 删除修订：该事件暂时不存在
}

// Sample 是一条取数样本。
type Sample struct {
	AccountID int64 // 账户
	T         int64 // 预测时刻，整数秒
	K         int64 // 数据可见截点，K <= T
}

// Result 是一条样本的计算结果。
type Result struct {
	Count int   // 窗口内可见事件数
	Sum   int64 // 窗口内可见事件金额总和（分）
}
