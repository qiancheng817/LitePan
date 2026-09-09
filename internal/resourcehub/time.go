package resourcehub

import "time"

// nowFunc 抽象时间来源，便于测试桩注入。
var nowFunc = func() time.Time { return time.Now() }