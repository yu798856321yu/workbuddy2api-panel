package upstream

import (
	"reflect"
	"testing"
)

// 这些测试锁住两路探测的合并语义（mergeModelInfos）。
//
// 背景 bug：CN 域 /v3/config 与 /console/enterprises/personal/models 会返回同一个模型，
// 但只有企业端点下发 reasoning.supportedEfforts。原先的合并是**模型级**覆盖
// （同 id 直接跳过 secondary），于是 v3 那条「有模型、无档位」的条目赢，
// 企业端点那 3 档被丢弃 → 客户端拿不到任何思考档位。
// 官方 app 走企业端点所以显示三档，网关却一档不显示，正是这个差异造成的。

func TestMergeModelInfosFillsEffortsFromSecondary(t *testing.T) {
	primary := []ModelInfo{{ID: "kimi-k2.8-preview"}} // v3：有模型、无档位
	secondary := []ModelInfo{{
		ID:            "kimi-k2.8-preview",
		Efforts:       []string{"low", "high", "max"},
		DefaultEffort: "high",
	}}

	out := mergeModelInfos(primary, secondary)
	if len(out) != 1 {
		t.Fatalf("模型数=%d want 1", len(out))
	}
	if !reflect.DeepEqual(out[0].Efforts, []string{"low", "high", "max"}) {
		t.Errorf("档位未从企业端点补齐: %v", out[0].Efforts)
	}
	if out[0].DefaultEffort != "high" {
		t.Errorf("默认档未同源补齐: %q", out[0].DefaultEffort)
	}
}

// v3 已下发档位时，企业端点不得覆盖——v3 优先语义不能被这次修复削弱。
func TestMergeModelInfosKeepsPrimaryEfforts(t *testing.T) {
	primary := []ModelInfo{{
		ID:            "glm-5.3",
		Efforts:       []string{"low", "high", "max"},
		DefaultEffort: "high",
	}}
	secondary := []ModelInfo{{
		ID:            "glm-5.3",
		Efforts:       []string{"medium"},
		DefaultEffort: "medium",
	}}

	out := mergeModelInfos(primary, secondary)
	if !reflect.DeepEqual(out[0].Efforts, []string{"low", "high", "max"}) {
		t.Errorf("v3 档位被企业端点覆盖: %v", out[0].Efforts)
	}
	if out[0].DefaultEffort != "high" {
		t.Errorf("v3 默认档被覆盖: %q", out[0].DefaultEffort)
	}
}

// 档位与默认档必须同源成组补齐：不得出现「档位来自企业端点、默认档来自 v3」的
// 跨源拼接——默认档必须落在同一张档位表里。
func TestMergeModelInfosEffortAndDefaultStaySameSource(t *testing.T) {
	primary := []ModelInfo{{ID: "m", DefaultEffort: "medium"}} // 有默认档但无档位表
	secondary := []ModelInfo{{
		ID:            "m",
		Efforts:       []string{"low", "high"},
		DefaultEffort: "low",
	}}

	out := mergeModelInfos(primary, secondary)
	if !reflect.DeepEqual(out[0].Efforts, []string{"low", "high"}) {
		t.Errorf("档位未补齐: %v", out[0].Efforts)
	}
	if out[0].DefaultEffort != "low" {
		t.Errorf("默认档=%q 未随档位同源切换（跨源拼接）", out[0].DefaultEffort)
	}
}

// 两路都没档位时保持无档位——不得凭空造档。
func TestMergeModelInfosNoEffortsWhenNeitherHasThem(t *testing.T) {
	out := mergeModelInfos(
		[]ModelInfo{{ID: "kimi-k3-1"}},
		[]ModelInfo{{ID: "kimi-k3-1"}},
	)
	if len(out[0].Efforts) != 0 {
		t.Errorf("两路均无档位却补出了: %v", out[0].Efforts)
	}
}

// 只在 primary 出现的模型原样保留；只在 secondary 出现的模型补进来（模型级补缺不变）。
func TestMergeModelInfosStillFillsMissingModels(t *testing.T) {
	out := mergeModelInfos(
		[]ModelInfo{{ID: "a"}, {ID: "b"}},
		[]ModelInfo{{ID: "b", Efforts: []string{"high"}}, {ID: "c"}},
	)
	ids := make([]string, len(out))
	for i, mi := range out {
		ids[i] = mi.ID
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Errorf("顺序/成员=%v want [a b c]（primary 原序在前，补充项在后）", ids)
	}
	if !reflect.DeepEqual(out[1].Efforts, []string{"high"}) {
		t.Errorf("b 的档位未补齐: %v", out[1].Efforts)
	}
}

// 空 ID 不得进入结果，也不得参与补缺索引。
func TestMergeModelInfosSkipsEmptyIDs(t *testing.T) {
	out := mergeModelInfos(
		[]ModelInfo{{ID: ""}, {ID: "a"}},
		[]ModelInfo{{ID: ""}, {ID: "b"}},
	)
	if len(out) != 2 || out[0].ID != "a" || out[1].ID != "b" {
		t.Errorf("空 ID 未被剔除: %+v", out)
	}
}

// secondary 为空是常见路径（v3 失败降级 / 企业端点失败），必须原样返回 primary。
func TestMergeModelInfosReturnsPrimaryWhenSecondaryEmpty(t *testing.T) {
	primary := []ModelInfo{{ID: "a", Efforts: []string{"low"}}}
	out := mergeModelInfos(primary, nil)
	if len(out) != 1 || out[0].ID != "a" {
		t.Fatalf("primary 未被原样返回: %+v", out)
	}
}

// 补缺索引对重复 id 取首条，与 seen 的「首次胜出」语义一致。
func TestMergeModelInfosFillIndexTakesFirstDuplicate(t *testing.T) {
	out := mergeModelInfos(
		[]ModelInfo{{ID: "m"}},
		[]ModelInfo{
			{ID: "m", Efforts: []string{"low"}},
			{ID: "m", Efforts: []string{"high"}},
		},
	)
	if !reflect.DeepEqual(out[0].Efforts, []string{"low"}) {
		t.Errorf("重复补源未取首条: %v", out[0].Efforts)
	}
}
