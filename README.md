<p align="center">
  <img src="https://raw.githubusercontent.com/DGZSbot/ai-icon/refs/heads/main/WorkBuddy.png" alt="WorkBuddy2API" width="120">
</p>

<h1 align="center">WorkBuddy2API Panel</h1>

<p align="center">
  <b>把 CodeBuddy 账号变成 OpenAI 兼容 API 的多账号网关 · 附 Web 管理面板</b><br>
  OAuth 浏览器登录 · 账号池轮转 · 熔断与冷却 · 会话粘性 · 定时签到 / 活跃 / 旅行 / 保活 · 成长任务一键完成 · <b>积分到期优先消耗</b> · <b>指定默认账号</b> · <b>实时请求与用量统计</b>
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.22.5-00ADD8?logo=go&logoColor=white&style=flat-square">
  <img alt="API" src="https://img.shields.io/badge/API-OpenAI_Compatible-412991?style=flat-square">
  <img alt="Deploy" src="https://img.shields.io/badge/Deploy-Single_Binary%20%7C%20Docker-2496ED?style=flat-square">
  <img alt="License" src="https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square">
</p>

---

## 这是什么

WorkBuddy2API 是一个自托管的 **OpenAI 兼容反向代理网关**：把 CodeBuddy（`copilot.tencent.com`）账号包装成统一的 `/v1/chat/completions` 服务，客户端用现有 OpenAI SDK 就能直接接入。

本项目基于 [linguo2625469/workbuddy2api-panel](https://github.com/linguo2625469/workbuddy2api-panel)（其上游为 [Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api)），基础网关能力全部保留，并在积分运营与用量观测上做了几处功能补齐：**积分到期时间能不能看到、快到期的积分能不能先用掉、这笔对话到底花了多少积分**。

面向**个人多账号**场景：多账号共享、单号故障自动换号、冷却 / 熔断防止雪崩、会话粘性保证多轮上下文不跳号。

> ⚠️ 本项目是**非官方**网关，上游接口均为逆向所得。仅限**本人授权账号**、本机 / 私有环境测试，请遵守 CodeBuddy 平台服务条款。

---

## 基础功能（继承自上游）

这些能力来自上游项目，本分支未改动其行为：

| 能力 | 说明 |
|---|---|
| 🔑 **OAuth 一键登录** | 面板「添加账号」走设备授权，凭证自动落盘并**热加载进池（免重启）** |
| 🔄 **多账号池** | 三因子加权随机选号（积分占比 + 闲置补偿 + 成功率），Top-5 候选 + 防惊群 |
| 🛡️ **熔断与冷却** | 429 软冷却指数退避、404 短冷却、402 硬冷却至次日 04:00、连续失败熔断、在途租约限流 |
| 🧲 **会话粘性** | 同一会话尽量绑定同一账号，TTL 滚动续期，失败自动解绑，可选 Redis 镜像防重启丢失 |
| ⏰ **定时任务** | 签到（含连登兑换 + 抽奖）、活跃上报、猫猫旅行、token 保活，四类独立开关 |
| ⚡ **流式 + 非流式** | 出站强制 `stream:true`；SSE 帧按规范白名单重建；非流式本地聚合为单响应 |
| 🧠 **推理模型兼容** | DeepSeek 思维链注入、`reasoning_content` 多轮回填、effort 档位自动降级 |
| 💬 **系统提示词体系** | 网关自有提示词替换客户端 system，减少内容误报；`passthrough` 遇拦截自动降级重试 |
| 🗑️ **指纹脱敏** | 出站请求体黑名单指纹字段清洗（可关闭） |
| 💾 **状态持久化** | 池状态本地原子落盘 + Upstash Redis 异步镜像（可选） |
| 🖥️ **Web 管理面板** | 内嵌单页面板（明暗主题）：账号运维 / 模型档位 / 在线改配置（热生效）/ 运行日志 |
| 🎯 **成长任务一键完成** | 官方 18 个成长任务中 17 个可纯 API 一键完成并自动领奖，无需安装客户端 |

---

## 新增功能（本分支改造）

### 1. 积分到期时间（修好了一个长期失效的功能）

**问题**：程序本来就在读「积分到期时间」，但读错了字段——它读的 `ExpiredTime` / `PackageEndTime` 在**有余额的积分包上恒为空**（实测 3 账号 27 个包全部为空），只有**已用完**的包才有值。所以界面上「剩余积分的到期时间」永远是空的。

**修复**：改读 `CycleEndTime`（有余额包的真实到期口径），旧字段保留作兜底。

实测（同一时刻、同一批账号）：修复前有余额的包带到期时间 **0 / 27**，修复后 **27 / 27**。

### 2. 优先消耗快到期的积分（两档分桶）

**问题**：选号逻辑里早就写了「谁家快过期积分占比高就优先用谁」的规则（权重 8.0），但它依赖上面那个恒为 0 的数据，所以**从未生效过**。数据源修好后该规则自动工作，并按到期紧迫度分成两档：

| 档位 | 配置项 | 默认 | 权重 | 含义 |
|---|---|---|---|---|
| 一档 | `pool.expiring_soon_7d` | `168h`（7 天） | ×9 | 一周内作废，最优先消耗 |
| 二档 | `pool.expiring_soon_15d` | `360h`（15 天） | ×5 | 两周内作废，次优先 |

**两档互斥**：≤7 天到期的积分只进一档，不重复进二档——否则同一笔积分会被加两次权重。两档之和始终 ≤ 总余额。窗口可在线改，空值 = 关闭该档。

### 3. 指定账号（面板「默认」开关）

账号池每行新增 `☆ 默认` / `★ 默认` 按钮，点一下设为默认账号，再点取消。

- 设了默认号后，普通请求**优先**使用它；
- 默认号不可用时（冷却 / 禁用 / 在途占满 / 该模型被限流）**静默回落到自动轮换**，请求不会失败；
- 同一时刻只有一个默认号，设置新号自动顶掉旧号；
- 设置随 `state.json` 持久化，重启后保留。

实现上默认号优先级**高于**「会话粘性」：原版粘性会在选号前直接返回旧账号，若不让路，默认开关会被完全绕过。现在的顺序是「默认号可用 → 用它；不可用 → 粘性照常生效」。

### 4. 对话消耗的积分

上游会在末帧 `usage` 里返回本次真实扣费 `credit`，原版把该字段丢弃了。现在解析并展示在三处：

- **请求日志**新增 `cr=` 列；
- **账号池**用量列显示累计扣费（如 `0.53cr`）；
- **用量统计**按账号 / 模型 / 域汇总（`credits` + `credit_samples`）。

口径说明：上游没返回 `credit` 时**不记为 0**（「没数据」与「免费」是两回事），因此同时给出样本数，便于判断均值是否可信。

### 5. 实时请求与用量统计

面板「用量」页新增：

- **实时请求表**：每 5 秒自动刷新，显示模型、模式、状态、账号、首帧延迟、token 数、速率、实际积分、总耗时。**固定高度的滚动列表**，表头滚动时钉在顶部，最多 200 条不会把整页撑长；
- **三张时序图**：token 消耗、积分消耗、平均延迟，支持 24 小时 / 3 天 / 7 天 / 30 天；
- **图表悬停看数值**：鼠标移到任意一根柱子上弹出该时段精确数值，整列都是命中区；
- 积分未被上游返回时显示 `—`，不会把「没采集到」画成「消耗为 0」。

### 改动文件清单

| 文件 | 改动 |
|---|---|
| `internal/upstream/client.go` | 统一到期口径；新增两档分桶 `ExpiringBuckets` |
| `internal/pool/entry.go` | 扣费累计字段；两档快过期与默认标记 |
| `internal/pool/pool.go` | `SetDefaultUID` / `DefaultUID` / `DefaultUIDIfUsable` |
| `internal/pool/pick.go` | 默认号优先 `pickDefaultLocked`；权重拆成 7d(9) / 15d(5) |
| `internal/pool/cooldown.go` | 两档扣费，含「两档之和 ≤ credits」钳制 |
| `internal/pool/persist.go` | 默认号与两档分桶持久化，旧单档字段迁移 |
| `internal/server/logging.go` | 解析 `usage.credit`；日志新增 `cr=` 列 |
| `internal/server/handler.go` | 默认号优先于会话粘性；扣费透出到用量记录 |
| `internal/usage/recent.go` | 最近 200 条结构化请求环形缓冲 |
| `internal/usage/usage.go` | 扣费按桶累计与聚合 |
| `internal/panel/panel.go` | 默认账号接口、签到/余额刷新带分桶、实时请求接口 |
| `internal/panel/app.js` | 默认开关、两档快过期标签、扣费展示、实时请求表与统计图 |
| `internal/panel/index.html` | 账号表「快过期」列；用量页实时请求与图表样式 |
| `cmd/server/config.go` | 两档配置项 `expiring_soon_7d` / `15d` + 旧键兼容 |

新增测试：`package_expiry_test.go`、`default_uid_test.go`、`default_usable_test.go`、`expiring_buckets_test.go`、`credit_logging_test.go`、`recent_test.go`。

---

## 快速开始

### 环境要求

- **Windows / macOS / Linux 直接跑单文件二进制**（推荐，无需 Docker），或
- **Docker + Docker Compose**，或
- 从源码构建：宿主机 Go ≥ 1.22

### 方式一：单文件运行（推荐）

```powershell
# 从源码构建
go build -trimpath -ldflags="-s -w" -o wb2api.exe ./cmd/server

# 首次启动自动生成 config.json（含随机 api_key，日志打印一次）
.\wb2api.exe -config config.json
```

exe 为**单文件自包含**（前端资源已 embed 进二进制），拷到任意机器即可运行，只需保证 `auths/`（凭证）与 `data/`（状态）目录可写。

### 方式二：Docker Compose

```bash
git clone https://github.com/yu798856321yu/workbuddy2api-panel.git
cd workbuddy2api-panel
cp config.example.json config.json
docker compose up -d --build
curl -s http://localhost:7863/healthz
```

### 打开面板

启动后访问 **`http://127.0.0.1:7863/panel/`**，首次会要求输入 `config.json` 里的 `api_key`，然后点右上角「**添加账号**」完成 OAuth 登录。

### 开发调试

```bash
go build ./...
go vet ./...
go test ./...
go run ./cmd/server -config config.json
```

---

## 配置说明（新增项）

```json
{
  "pool": {
    "expiring_soon_7d": "168h",
    "expiring_soon_15d": "360h"
  }
}
```

两项都可留空以关闭对应档位。也可在面板「配置 → 账号池」在线修改，保存即生效，无需重启。

---

## API 端点

| 端点 | 鉴权 | 说明 |
|---|---|---|
| `POST /v1/chat/completions` | Bearer | OpenAI 兼容补全，流式 / 非流式 |
| `GET /v1/models` | Bearer | 模型列表，附带实际思考档位与积分倍率 |
| `GET /status` | Bearer | 账号状态汇总 + 每账号详情 |
| `GET /healthz` | 无 | 健康检查，有可用账号返回 200，否则 503 |

面板接口挂在 `/panel/api/*`（同一 Bearer 鉴权），其中新增 `GET /panel/api/recent`（最近请求）与 `POST /panel/api/accounts/{uid}/default`（设默认账号）。

---

## 免责声明

本项目仅供学习和研究使用。使用者需遵守 CodeBuddy 服务条款，自行承担使用风险（包括账号封禁、条款违约等）。作者不对任何因使用本项目产生的直接或间接损失负责。

## 致谢与许可

本项目基于以下开源项目：

- [Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api) —— 原始网关实现
- [linguo2625469/workbuddy2api-panel](https://github.com/linguo2625469/workbuddy2api-panel) —— Web 管理面板与任务体系

采用 [MIT License](LICENSE) 开源。再分发时请保留原仓库的 MIT 版权声明。
