package server

import (
	"encoding/json"
	"testing"

	"github.com/yu798856321yu/workbuddy2api-panel/internal/upstream"
)

// 这些测试锁住 /v1/models 的「能力字段双名输出」契约。
//
// 背景：本产品历史上只输出私有字段名（supports_images / reasoning_supported_efforts），
// 而下游 OpenAI 兼容网关（opencodex 等）的目录解析器只认通用名
// （input_modalities / reasoning_efforts / capabilities.*）。字段名对不上时下游静默丢弃，
// 表现为「模型获取不到思考等级和多模态配置」——不报错、只是能力凭空消失。
// 因此这里同时断言两个名字都在，防止后续重构把兼容名删掉。

// entryOf 把 map 走一遍 JSON 往返，模拟真实 HTTP 序列化后的形态，
// 避免直接读 map 时漏掉「值是 Go 特有类型、序列化后变样」的问题。
func entryOf(t *testing.T, entry map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal entry: %v", err)
	}
	return out
}

// TestApplyModelInfoFieldsEmitsCompatModalities 多模态能力必须同时写私有名与通用名。
func TestApplyModelInfoFieldsEmitsCompatModalities(t *testing.T) {
	entry := entryOf(t, applyModelInfoFields(map[string]any{"id": "cn:glm-5.3"}, upstream.ModelInfo{
		ID:             "glm-5.3",
		SupportsImages: true,
	}))

	if entry["supports_images"] != true {
		t.Errorf("私有名 supports_images 丢失: %v", entry["supports_images"])
	}
	mods, ok := entry["input_modalities"].([]any)
	if !ok {
		t.Fatalf("通用名 input_modalities 缺失或类型错误: %#v", entry["input_modalities"])
	}
	if len(mods) != 2 || mods[0] != "text" || mods[1] != "image" {
		t.Errorf("input_modalities=%v want [text image]", mods)
	}
}

// TestApplyModelInfoFieldsOmitsCompatModalitiesWhenNoImages 上游未声明多模态时
// 不得编造通用名——下游读不到即按纯文本处理，与字段缺失的既有行为一致。
func TestApplyModelInfoFieldsOmitsCompatModalitiesWhenNoImages(t *testing.T) {
	entry := entryOf(t, applyModelInfoFields(map[string]any{"id": "cn:hy4-preview"}, upstream.ModelInfo{
		ID:             "hy4-preview",
		SupportsImages: false,
	}))

	if _, present := entry["input_modalities"]; present {
		t.Errorf("未声明多模态却输出了 input_modalities: %v", entry["input_modalities"])
	}
	if _, present := entry["supports_images"]; present {
		t.Errorf("未声明多模态却输出了 supports_images")
	}
}

// TestApplyEffortCompatFieldsEmitsBothNames 档位必须同时写私有名与通用名，
// 且默认档只在命中档位时输出。
func TestApplyEffortCompatFieldsEmitsBothNames(t *testing.T) {
	entry := map[string]any{}
	applyEffortCompatFields(entry, []string{"low", "high", "max"}, "high")
	got := entryOf(t, entry)

	for _, key := range []string{"reasoning_supported_efforts", "reasoning_efforts"} {
		arr, ok := got[key].([]any)
		if !ok {
			t.Fatalf("%s 缺失或类型错误: %#v", key, got[key])
		}
		if len(arr) != 3 || arr[0] != "low" || arr[1] != "high" || arr[2] != "max" {
			t.Errorf("%s=%v want [low high max]", key, arr)
		}
	}
	if got["reasoning_default_effort"] != "high" {
		t.Errorf("reasoning_default_effort=%v want high", got["reasoning_default_effort"])
	}
}

// TestApplyEffortCompatFieldsNilEffortsWritesNothing 无档位（nil）时必须整体省略，
// 不能输出空数组——下游对「空数组」与「字段缺失」的处理不同。
func TestApplyEffortCompatFieldsNilEffortsWritesNothing(t *testing.T) {
	entry := map[string]any{}
	applyEffortCompatFields(entry, nil, "")

	for _, key := range []string{"reasoning_supported_efforts", "reasoning_efforts", "reasoning_default_effort"} {
		if _, present := entry[key]; present {
			t.Errorf("无档位却输出了 %s: %#v", key, entry[key])
		}
	}
}

// TestApplyEffortCompatFieldsNoDefaultWhenUnmatched 默认档不命中档位表时只输出档位，
// 不输出一个模型并不支持的默认档（对齐 EffortListing 的既有防御）。
func TestApplyEffortCompatFieldsNoDefaultWhenUnmatched(t *testing.T) {
	entry := map[string]any{}
	applyEffortCompatFields(entry, []string{"medium"}, "")

	if _, present := entry["reasoning_default_effort"]; present {
		t.Errorf("默认档未命中却输出: %v", entry["reasoning_default_effort"])
	}
	if arr, ok := entry["reasoning_efforts"].([]string); !ok || len(arr) != 1 || arr[0] != "medium" {
		t.Errorf("reasoning_efforts=%#v want [medium]", entry["reasoning_efforts"])
	}
}
