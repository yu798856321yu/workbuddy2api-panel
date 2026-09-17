// recent.go 最近请求的结构化环形缓冲（面板「实时请求」用）。
//
// 与 panel.Ring 的区别：那个存的是**日志文本**（按行归类频道，供日志视图），
// 本文件存的是**结构化字段**（模型/token/扣费/延迟/账号），供面板直接渲染表格，
// 不必反向解析日志行的文本格式（格式一变解析就碎）。
//
// 放在 usage 包而非 server 包：server 与 panel 都依赖 usage，环形缓冲落在这里
// 两边都能用，且不引入 panel → server 的新依赖方向（保持原有分层）。
// 容量固定、FIFO 淘汰，仅保留最近 N 条；不落盘、不参与任何路由决策。
package usage

import (
	"sync"
	"time"
)

// RecentRequest 单条请求的观测记录（脱敏：uid 只留前 8 位，无任何 token 明文）。
type RecentRequest struct {
	Seq          int64     `json:"seq"`   // 进程级序号，前端据此判断"有没有新数据"
	At           time.Time `json:"at"`    // 请求结束时刻
	Model        string    `json:"model"` // 请求模型（含 realm 前缀）
	Mode         string    `json:"mode"`  // "stream" | "sync"
	Status       int       `json:"status"`
	UID          string    `json:"uid"`     // 前 8 位
	TTFBMs       int64     `json:"ttfb_ms"` // 流式首帧耗时；非流式为 0
	Tokens       int       `json:"tokens"`  // completion_tokens；<0 = 上游未给
	TokensPerSec float64   `json:"tokens_per_sec"`
	// Credits 本次真实扣费（usage.credit）；HasCredits=false 表示上游没给该字段，
	// 与"扣了 0"是两回事，前端据此显示 — 而不是 0。
	Credits    float64 `json:"credits"`
	HasCredits bool    `json:"has_credits"`
	TotalMs    int64   `json:"total_ms"`
}

// recentCap 最近请求的保留条数。面板一次展示几十条，200 条足够覆盖
// "刚发起的请求"这个观察窗口，又不会让 JSON 响应过大。
const recentCap = 200

// recentRing 最近请求环形缓冲（包级单例：进程内只有一份观测流）。
type recentRing struct {
	mu   sync.Mutex
	buf  []RecentRequest
	next int  // 下一个写入位置
	full bool // 是否已绕圈（决定快照的排序起点）
}

var recent recentRing

// AddRecent 记录一条请求（chat 请求出口调用，幂等无副作用）。
func AddRecent(r RecentRequest) {
	recent.mu.Lock()
	defer recent.mu.Unlock()
	if recent.buf == nil {
		recent.buf = make([]RecentRequest, recentCap)
	}
	recent.buf[recent.next] = r
	recent.next = (recent.next + 1) % recentCap
	if recent.next == 0 {
		recent.full = true
	}
}

// RecentRequests 返回最近的请求记录，**最新的在前**（前端表格直接顺序渲染）。
func RecentRequests() []RecentRequest {
	recent.mu.Lock()
	defer recent.mu.Unlock()
	if len(recent.buf) == 0 {
		return nil
	}
	n := recent.next
	if recent.full {
		n = recentCap
	}
	out := make([]RecentRequest, 0, n)
	// 从最新往回读：next-1 是最后写入的位置。
	for i := 0; i < n; i++ {
		idx := (recent.next - 1 - i + recentCap) % recentCap
		out = append(out, recent.buf[idx])
	}
	return out
}

