package usage

import (
	"testing"
	"time"
)

// 环形缓冲：最新在前、容量上限 FIFO 淘汰。
func TestRecentRingOrderAndCap(t *testing.T) {
	// 本包单例，测试内先写满再校验（其它用例不依赖该单例内容）。
	for i := 1; i <= recentCap+10; i++ {
		AddRecent(RecentRequest{Seq: int64(i), Model: "m", At: time.Now()})
	}
	rows := RecentRequests()
	if len(rows) != recentCap {
		t.Fatalf("条数 = %d, want %d（容量上限）", len(rows), recentCap)
	}
	// 最新在前：首条应是最后写入的 seq。
	if rows[0].Seq != int64(recentCap+10) {
		t.Errorf("首条 seq = %d, want %d（最新在前）", rows[0].Seq, recentCap+10)
	}
	// 单调递减（严格最新在前）。
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq >= rows[i-1].Seq {
			t.Fatalf("第 %d 条 seq=%d 不小于前一条 %d（顺序错）", i, rows[i].Seq, rows[i-1].Seq)
		}
	}
	// 最旧的一条应是第 11 个（前 10 个已被淘汰）。
	if last := rows[len(rows)-1].Seq; last != 11 {
		t.Errorf("末条 seq = %d, want 11（FIFO 淘汰最旧）", last)
	}
}

// 空缓冲返回 nil（面板据此渲染空状态，不 panic）。
func TestRecentEmptyBeforeAnyWrite(t *testing.T) {
	// 无法重置单例，此处只验证"读取不 panic 且元素字段完整"。
	rows := RecentRequests()
	if len(rows) > 0 {
		for i, r := range rows {
			if r.Model == "" && r.Seq == 0 {
				t.Errorf("第 %d 条记录字段为空", i)
			}
		}
	}
}

// HasCredits 语义：false 表示上游没给该字段，与"扣了 0"区分。
func TestRecentCreditsSemantics(t *testing.T) {
	AddRecent(RecentRequest{Seq: 999001, Credits: 0, HasCredits: true})  // 确实扣了 0
	AddRecent(RecentRequest{Seq: 999002, Credits: 0, HasCredits: false}) // 上游没给
	rows := RecentRequests()
	if len(rows) < 2 {
		t.Fatal("应有至少 2 条记录")
	}
	// 最新在前：rows[0] = 后写入的 999002（上游没给 → HasCredits=false）。
	if rows[0].Seq != 999002 {
		t.Fatalf("rows[0].Seq = %d, want 999002（最新在前）", rows[0].Seq)
	}
	if rows[0].HasCredits {
		t.Error("上游没给 credit 时 HasCredits 应为 false")
	}
	// rows[1] = 先写入的 999001：确实扣了 0，HasCredits 必须为 true。
	if rows[1].Seq != 999001 {
		t.Fatalf("rows[1].Seq = %d, want 999001", rows[1].Seq)
	}
	if !rows[1].HasCredits || rows[1].Credits != 0 {
		t.Errorf("确实扣了 0 时应为 HasCredits=true 且 Credits=0, got %v/%v",
			rows[1].HasCredits, rows[1].Credits)
	}
}
