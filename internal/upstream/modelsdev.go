// modelsdev.go models.dev 按需兜底（context_length 四级查找链的第 4 级）。
//
// 定位：只对「上游动态值缺失 + 静态种子表未收录 + model.json 未缓存」的模型查
// models.dev，是兜底的兜底——超时短（默认 5s）、失败静默降级（context_length 落
// DefaultContextWindow=1M），绝不阻塞 /v1/models 主路径（查找异步化，本次请求
// 直接返回兜底值，拉到后写 model.json 供下次命中）。
//
// 数据源（2026-09-16 逆向，结论详见 .claude/reports/model-json-dynamic.md）：
//   - 官方聚合 JSON 端点 https://models.dev/api.json：~4.7MB 单文档、217 provider、
//     免鉴权、Cloudflare 托管静态站；
//   - 无按模型/按 provider 子端点（/z-ai.json 等 302 回 /），「按需」的实现是
//     单次拉全量文档 + 建裸 id 索引（拉一次只发生一次，此后进程内复用索引）；
//   - schema：{ "<provider>": { "models": { "<id>": { "limit": {"context": N,
//     "output": N} } } } }，模型 id 有裸名（glm-5.2）与带命名空间（openai/gpt-5.5）
//     两种形态，均取尾段做索引 key；
//   - 多 provider 同名值会分歧（聚合网关常自报改动）：vendor 官方源（zai/
//     moonshotai/openai/google/deepseek/minimax）优先，其余取众数（共识值）。
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ModelsDevURL models.dev 官方聚合 JSON 端点（唯一端点，见文件头逆向结论）。
const ModelsDevURL = "https://models.dev/api.json"

// modelsDevTimeout 单次拉取超时：兜底的兜底，不值得等（任务书 §2：如 5s）。
const modelsDevTimeout = 5 * time.Second

// modelsDevFetchCooldown 拉取节流（进程级）：文档是全量聚合体，5min 内不重拉
// （同模型 24h 负缓存之外的整体节流，防短窗反复打 models.dev）。
const modelsDevFetchCooldown = 5 * time.Minute

// modelsDevNegativeTTL 同模型负缓存：查不到的模型 24h 内不重查
// （任务书 §2：如同模型 24h 内不重查，查不到的模型负缓存防反复打）。
const modelsDevNegativeTTL = 24 * time.Hour

// modelsDevMaxBody 拉取响应体上限（文档实测 ~4.7MB，留余量；防异常大响应拖死）。
const modelsDevMaxBody = 32 << 20

// modelsDevValueMax 值校验上限：context/output 超过 1e9 视为脏数据拒绝
// （量级上限校验，任务书 §2 值校验；正数下界在 catalog 写入侧兜底）。
const modelsDevValueMax = int64(1e9)

// modelsDevVendorSources 官方 vendor provider 优先名单：models.dev 收录 217 个
// provider，聚合网关（merge-gateway/nano-gpt 等）自报的 limit 常与官方源分歧，
// 采值优先级 = 本名单命中 > 众数共识。
var modelsDevVendorSources = map[string]bool{
	"zai":           true, // Z.AI（glm 家族官方）
	"moonshotai":    true, // Moonshot AI（kimi 家族官方，国际版）
	"moonshotai-cn": true, // Moonshot AI 中国版
	"openai":        true,
	"google":        true,
	"deepseek":      true,
	"minimax":       true,
}

// modelsDevEntry models.dev 单模型采值结果（modelsdev json 的 limit 子集）。
type modelsDevEntry struct {
	Context int64
	Output  int64
}

// modelsDevFetcher models.dev 按需拉取器：进程级单例语义（包级变量 modelsDev），
// 拉取节流 + 裸 id 索引缓存 + 同模型负缓存。测试用 resetModelsDev / 独立 base URL
// 注入隔离（newModelsDevForTest）。
type modelsDevFetcher struct {
	mu sync.Mutex

	// doc 文档解析后的裸 id 索引（多 provider 同名合并采值：vendor 优先/众数）。
	// nil = 未拉取；空 map（非 nil）= 拉取过但索引为空（视作失败冷却）。
	doc map[string]modelsDevEntry

	lastFetch time.Time // 最近一次拉取尝试（成功与失败都算，冷却节流）
	fetched   bool      // 是否已拉取过（doc 字段区分成败）

	negatives map[string]time.Time // 查询未命中的模型 → 记录时间（24h 负缓存）
}

// modelsDev 包级拉取器实例（单例：全进程共享一份文档索引与节流状态）。
var modelsDev = &modelsDevFetcher{}

// resetModelsDev 测试隔离：清空单例状态（doc/fetched/lastFetch/negatives）。
func resetModelsDev() {
	modelsDev.mu.Lock()
	modelsDev.doc = nil
	modelsDev.fetched = false
	modelsDev.lastFetch = time.Time{}
	modelsDev.negatives = nil
	modelsDev.mu.Unlock()
}

// lookup 查询一个模型的 (context, output, found)：
//   - 索引命中且值合法 → found=true；
//   - 索引未命中（含索引尚未就绪）→ 记入 negatives（既是 24h 负缓存，也是
//     fetchDoc 成功后 backfillMisses 的回流清单——「曾 miss 过的模型」），found=false。
//
// 只读内存索引，不发网络请求；网络动作由 ensureDocAsync（goroutine 内）负责。
// 注意：doc 就绪前的 miss 也记 negatives——backfillMisses 回流时查到即写
// model.json 并清除负缓存条目（freshLookup），查不到的保持 24h 负缓存。
func (f *modelsDevFetcher) lookup(model string) (modelsDevEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.doc != nil {
		if e, ok := f.doc[model]; ok {
			return e, true
		}
	}
	// 未命中（索引在但模型不在，或索引尚未就绪）：记 miss。
	if f.negatives == nil {
		f.negatives = make(map[string]time.Time)
	}
	f.negatives[model] = time.Now()
	return modelsDevEntry{}, false
}

// negativeFresh 模型是否在负缓存有效期内（供查找链短路第 4 级触发）。
func (f *modelsDevFetcher) negativeFresh(model string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.negatives[model]
	return ok && time.Since(t) < modelsDevNegativeTTL
}

// ensureDocAsync 确保 models.dev 文档索引可用（异步，不阻塞调用方）：
// 已有索引 / 拉取冷却期内 / 已有人在拉（in-flight 去重）→ 直接返回。
// 否则起 goroutine 拉取解析，完成后落 f.doc（失败静默：只刷新 lastFetch 冷却，
// 下次查找仍走 1M 兜底，不重试风暴）。
func (f *modelsDevFetcher) ensureDocAsync(client *http.Client, baseOverride string) {
	f.mu.Lock()
	if f.doc != nil {
		f.mu.Unlock()
		return
	}
	if f.fetched && time.Since(f.lastFetch) < modelsDevFetchCooldown {
		// 拉取过（成功或失败）且冷却期内：不再打 models.dev。
		f.mu.Unlock()
		return
	}
	// in-flight 去重：把 fetched/lastFetch 先置为「本次进行中」，
	// 后续并发调用在冷却窗口内直接返回，不重复起拉取。
	f.fetched = true
	f.lastFetch = time.Now()
	f.mu.Unlock()

	go f.fetchDoc(client, baseOverride)
}

// fetchDoc 拉取并解析 models.dev 文档，建裸 id 索引（goroutine 内执行，永不 panic
// 上抛：任何失败只静默冷却）。
func (f *modelsDevFetcher) fetchDoc(client *http.Client, baseOverride string) {
	if client == nil {
		client = http.DefaultClient
	}
	url := ModelsDevURL
	if baseOverride != "" {
		url = baseOverride
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelsDevTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Printf("WARN: [upstream] models.dev fetch: build request: %v", err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("WARN: [upstream] models.dev fetch failed (silent fallback to 1M): %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("WARN: [upstream] models.dev fetch status %d (silent fallback to 1M)", resp.StatusCode)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsDevMaxBody))
	if err != nil {
		log.Printf("WARN: [upstream] models.dev fetch read: %v", err)
		return
	}
	doc, err := parseModelsDevDoc(raw)
	if err != nil {
		log.Printf("WARN: [upstream] models.dev parse failed (silent fallback to 1M): %v", err)
		return
	}
	f.mu.Lock()
	f.doc = doc
	f.mu.Unlock()
	// 文档就绪后把「曾 miss 过的模型」回流 model.json（第 4 级 → 第 3 级，
	// 下次 /v1/models 直接命中缓存）。仍查不到的保持负缓存。
	f.backfillMisses()
}

// parseModelsDevDoc 解析 models.dev api.json：{provider:{models:{id:{limit:{context,
// output}}}}} → 裸 id 索引。同名多 provider 采值：官方 vendor 源（modelsDevVendorSources）
// 优先；无官方源（或多官方源分歧——理论罕见，取先到的）取众数（出现次数最多的值对）。
func parseModelsDevDoc(raw []byte) (map[string]modelsDevEntry, error) {
	var doc map[string]struct {
		Models map[string]struct {
			Limit *struct {
				Context int64 `json:"context"`
				Output  int64 `json:"output"`
			} `json:"limit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("models.dev doc: %w", err)
	}
	// 同名 id 的候选值收集：vendorOfficial 标记官方源，votes 计众数。
	type candidate struct {
		entry  modelsDevEntry
		vendor bool
		votes  int
		aggKey string // 去重聚合 key（同值多 provider 只计票不重复存）
	}
	byModel := map[string][]candidate{}
	for provider, pv := range doc {
		for fullID, mv := range pv.Models {
			if mv.Limit == nil {
				continue
			}
			id := fullID
			if i := strings.LastIndex(fullID, "/"); i >= 0 {
				id = fullID[i+1:]
			}
			if id == "" {
				continue
			}
			// 值校验（任务书 §2）：正数 + 量级上限，脏值不进索引。
			ctx, out := mv.Limit.Context, mv.Limit.Output
			if ctx <= 0 || ctx > modelsDevValueMax {
				continue
			}
			if out < 0 || out > modelsDevValueMax {
				continue
			}
			key := fmt.Sprintf("%d/%d", ctx, out)
			cs := byModel[id]
			dup := false
			for i := range cs {
				if cs[i].aggKey == key {
					cs[i].votes++
					if modelsDevVendorSources[provider] {
						cs[i].vendor = true
					}
					dup = true
					break
				}
			}
			if !dup {
				byModel[id] = append(cs, candidate{
					entry:  modelsDevEntry{Context: ctx, Output: out},
					vendor: modelsDevVendorSources[provider],
					votes:  1,
					aggKey: key,
				})
			}
		}
	}
	out := make(map[string]modelsDevEntry, len(byModel))
	for id, cs := range byModel {
		best := 0
		for i, c := range cs {
			// 优先级：官方 vendor 源 > 票数众数 > 先出现。
			cur := cs[best]
			better := false
			if c.vendor && !cur.vendor {
				better = true
			} else if c.vendor == cur.vendor && c.votes > cur.votes {
				better = true
			}
			if better {
				best = i
			}
		}
		out[id] = cs[best].entry
	}
	return out, nil
}
