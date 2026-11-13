# CPA Ollama Plugin

一个 CLIProxyAPI（CPA）插件：**把 [Ollama Cloud](https://ollama.com) 的 API Key 作为 CPA 认证文件接入，原生转发 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages 三种协议。**

- 每个 Ollama API Key 是一个认证文件（`{"type":"ollama","api_key":"..."}`），和其他凭证一样参与 CPA 的选号、冷却和故障转移；
- 自动拉取 Ollama 云端模型目录（`/api/tags` + `/api/show`）并注册到 CPA，带上下文长度、视觉 / 工具 / 思考能力；前缀走 CPA 通用的 `prefix` 机制，默认 `ollama/<模型 ID>`；每个 Key 可以只注册选中的模型；
- 请求按客户端格式原样转发到 Ollama 对应的接口（`https://ollama.com/v1/...`），不做协议转换；
- 通过 Ollama 的 [余额](https://docs.ollama.com/api/balance) 和 [用量](https://docs.ollama.com/api/cloud-usage) 接口查询账户：套餐额度剩余、购买的额度、近 24 小时 / 7 天 / 30 天用量和按模型拆分，CPA 原生额度面板和插件管理页都能看到；
- 每个 Key 可以单独配置出口代理（CPA 通用认证文件字段 `proxy_url`）；
- 自带管理页：添加、编辑、启停、删除 Key，选择上游模型，查看余额和用量，管理代理列表，查看请求日志。

> 这是社区插件，与 Ollama 官方无关。Ollama 名称和羊驼图标归 Ollama 所有，这里只用来标识所接入的服务。

## 环境要求

- CLIProxyAPI v8（按 v8.0.58 的插件 SDK 构建），且开启了动态插件支持
- Linux amd64/arm64、macOS amd64/arm64 或 Windows amd64
- Unix 平台上需要启用 cgo 编译的 CPA
- 一个 Ollama 账号，以及在 [ollama.com/settings/keys](https://ollama.com/settings/keys) 创建的 API Key

## 安装

### 通过插件商店

在 CPA 配置里加上 berry-shake 的商店源：

```yaml
plugins:
  enabled: true
  store-sources:
    - https://raw.githubusercontent.com/berry-shake/CLIProxyAPI-Plugins-Store/main/registry.json
```

然后在管理面板的插件商店里找到 **Ollama**（ID `ollama`）安装，也可以直接调用管理接口：

```bash
curl -X POST -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  -d '{"version":"0.1.0"}' \
  http://127.0.0.1:8317/v0/management/plugin-store/ollama/install
```

### 手动安装

1. 从 [Release](https://github.com/berry-shake/cpa-ollama-plugin/releases) 下载对应平台的 zip，解压出 `ollama.<so|dylib|dll>`。
2. 放进对应平台的插件目录，例如：

   ```text
   plugins/linux/amd64/ollama.so
   plugins/linux/arm64/ollama.so
   plugins/darwin/amd64/ollama.dylib
   plugins/darwin/arm64/ollama.dylib
   plugins/windows/amd64/ollama.dll
   ```

3. 在 `config.yaml` 里启用：

   ```yaml
   plugins:
     enabled: true
     configs:
       ollama:
         enabled: true
   ```

> 如果 `config.yaml` 里已经有 `store.version`，CPA 只会加载文件名版本和它一致的插件文件。手动换文件时记得同时改 `store.version` 和 `release-tag`。

## 插件配置

所有配置项都是可选的，写在 `plugins.configs.ollama` 下面：

```yaml
plugins:
  configs:
    ollama:
      enabled: true
      base-url: https://ollama.com        # Ollama 地址，末尾的 /v1 或 /api 会自动去掉
      model-prefix: ollama                # 认证文件没写 prefix 时使用的默认前缀；写 "" 表示不加前缀
      models-refresh-minutes: 60          # 模型目录轮询间隔（分钟），0 = 不轮询，只按需加载
      request-timeout-seconds: 600        # 非流式请求超时
      stream-idle-timeout-seconds: 300    # 流式响应多久没有新数据就中断
      data-dir: plugins/ollama-data       # 插件数据目录（请求日志数据库 ollama.db、代理列表 proxies.json），相对路径按 CPA 的工作目录算
      log-retention: 100000               # 请求日志最多保留多少条（上限 1000000），0 = 不记录
      log-retention-days: 30              # 请求日志保留多少天，0 = 不按天数清理，只受条数限制
```

非法的值会被忽略并回退到默认值，同时写一条警告日志，不会让插件注册失败。

## 认证文件

放在 CPA 认证目录里的 JSON 文件，`type` 为 `ollama` 的文件由本插件接管。通过管理页添加的 Key 会保存为 `ollama-<Key 指纹>.json`，文件名不包含 Key 本身。

```json
{
  "type": "ollama",
  "api_key": "<Ollama API Key>",
  "label": "主账号",
  "models": ["gpt-oss:120b", "qwen3-coder:480b"],
  "prefix": "ollama",
  "proxy_url": "socks5://127.0.0.1:1080",
  "priority": 0,
  "note": "",
  "disabled": false
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `type` | 是 | 固定为 `ollama` |
| `api_key` | 是 | Ollama API Key（也接受 `api-key`、`key`） |
| `label` | 否 | 显示名称；为空时用认证文件名（不带 `.json`） |
| `models` | 否 | 要注册的上游模型 ID 列表。**不写或为 `null` 表示注册目录里的全部对话模型**；`[]` 表示一个都不注册 |
| `prefix` | 否 | CPA 通用的模型前缀字段。不写时使用插件配置的 `model-prefix`；`""` 表示这个 Key 不加前缀 |
| `proxy_url` | 否 | 出口代理，见下文 |
| `base_url` | 否 | 覆盖插件配置里的 `base-url`，一般不需要 |
| `priority` | 否 | CPA 通用的凭证优先级 |
| `note` | 否 | 备注 |
| `disabled` | 否 | CPA 通用的停用开关 |
| `model_revision` | — | 插件自己维护的目录版本标记，不要手动修改 |

### 模型前缀

前缀使用 CPA 通用的 `prefix` 字段，由 CPA 自己加到注册的模型名上，转发前再去掉。前缀为 `ollama` 时，客户端请求 `ollama/gpt-oss:120b`，Ollama 收到的是 `gpt-oss:120b`。

- 认证文件没写 `prefix` 时，插件把配置里的 `model-prefix`（默认 `ollama`）作为这个 Key 的前缀交给 CPA，不会写进文件；改了 `model-prefix` 后插件会重新保存所有 Ollama 认证文件，让 CPA 按新前缀重新注册。
- 写了 `prefix` 就用文件里的值，`""` 表示不加前缀。在管理页或 CPA 面板里修改认证文件的 `prefix`，效果相同。

> CPA 的 `routing.force-model-prefix` 默认是 `false`。这时 CPA 会同时注册带前缀和不带前缀的两个名字（`ollama/gpt-oss:120b` 和 `gpt-oss:120b`），Ollama 的 Key 也会进入同名模型的公共号池，和其他渠道一起轮询。如果只想通过 `ollama/...` 访问 Ollama，在 CPA 配置里打开：
>
> ```yaml
> routing:
>   force-model-prefix: true
> ```
>
> 这是全局开关，打开后其他配置了 `prefix` 的凭证也只会注册带前缀的名字。管理页的"注册到 CPA 的模型名"预览会按这个开关显示。

选中的模型如果暂时不在 Ollama 目录里（比如上游下线了），仍然会注册，管理页会把它标成"已不在模型目录中"。

### 出口代理

`proxy_url` 是 CPA 认证文件的通用字段，本插件的所有上游请求（对话、模型目录、余额、用量）都按以下顺序选择出口：

1. 认证文件里的 `proxy_url`；
2. CPA 全局配置的 `proxy-url`；
3. 环境变量 `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY`。

支持 `http://`、`https://`、`socks5://`、`socks5h://`，可以带用户名密码。写 `direct` 或 `none` 表示这个 Key 强制直连，不走全局代理。管理页里显示的代理地址会隐藏密码。

常用的代理可以在管理页的 **代理** 页里存成列表，编辑 Key 时直接从列表里选。选列表里的代理，实际上就是把它的地址写进 `proxy_url`，CPA 那边看到的还是普通的认证文件字段。

## 模型同步

- 插件启动后按 `models-refresh-minutes` 轮询 `GET /api/tags`（无需鉴权，接口不带 ETag，每次都完整拉取）。
- 新出现的模型、以及 `digest` 变了的模型，会再调用 `POST /api/show` 读取详情：上下文长度、模型家族、参数量、量化方式、思考级别（`low` / `medium` / `high` 或开关）。详情按 digest 缓存，最多 4 个请求并发；某个模型详情拉取失败时保留它上一次的详情，下次再试。
- "全部模型" 只注册对话模型（带 `completion` 能力的模型）；向量等非对话模型在模型目录页会标出来，需要的话可以在"只用选中的模型"里手动勾选。
- 注册信息包括上下文长度、输入模态（带 `vision` 能力的模型加上图片输入）和支持的参数（`tools`、`reasoning`）。模型增减、上下文长度或能力变化时，插件会重新保存受影响的认证文件，CPA 随即重新注册这些 Key 的模型。
- 目录拉取失败时保留上一次的目录，不会把已注册的模型清空。
- 管理页的"重新拉取模型目录"会重新拉取所有模型的详情。

## 请求转发

| 客户端格式 | Ollama 接口 |
|---|---|
| OpenAI Chat Completions | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| Anthropic Messages | `POST /v1/messages` |

- 所有接口都用 `Authorization: Bearer <api_key>` 鉴权（Ollama 的 Messages 接口不认 `x-api-key`）。
- 请求体只改写顶层的 `model` 字段，其余字节原样转发；
- Chat Completions 流式请求会自动加上 `stream_options.include_usage`，让 CPA 能统计用量；
- `anthropic-version` / `anthropic-beta` 只转发给 Messages 接口，客户端没带 `anthropic-version` 时补 `2023-06-01`；
- Ollama 的 Responses 接口只支持无状态调用，`previous_response_id` 等依赖服务端存储的字段由 Ollama 自己报错；
- Ollama 没有 `count_tokens` 接口，Anthropic 的 `/v1/messages/count_tokens` 请求直接返回 501（`unsupported_operation`）。

流式响应：

- 开头几帧（`message_start`、`response.created`、只带角色的 chunk、ping）先攒着，等到第一帧真正的输出再一起发给 CPA。这期间如果 Ollama 在流里报错（限流、过载等），这次调用按对应状态码失败，CPA 会和 HTTP 报错一样冷却 Key 或换 Key 重试；攒的内容超过 64KB 就不再等。
- 已经开始输出以后的错误帧原样转给客户端。
- 流没收到结束帧就断了（Chat 的 `[DONE]` 或 `finish_reason`、Responses 的 `response.completed`、Messages 的 `message_stop`），按失败处理，错误码 `stream_truncated`（502）。CPA 对这个错误码不冷却 Key。

上游错误到 CPA 的映射：

| Ollama 返回 | 交给 CPA | 效果 |
|---|---|---|
| 401 / 402 / 403 | 原状态码 | 这个 Key 进入冷却 |
| 404（模型不存在） | 400 | 只失败这一次请求，不冷却 Key |
| 429（限流或套餐用量到顶） | 429，保留 `Retry-After` | 按上游给的时间冷却 |
| 502（云端模型暂时不可用） | 502 | 可重试 |
| 529 | 503 | 可重试 |
| 499 | 408 | 可重试 |

## 余额和用量

查询一个 Key 的账户时，插件按顺序调用三次 Ollama 接口：

1. `GET /api/balance`：套餐额度（本周期剩余 / 总额，以及周期结束时间）和购买的额度；老套餐返回的是会话限额和每周限额的剩余百分比；
2. `GET /api/usage?range=30d&group_by=model`：近 30 天用量（30 个完整 UTC 日加今天），以及用量最大的 10 个模型和其余模型的合计；
3. `GET /api/usage?range=24h`：近 24 小时用量。

近 7 天用量从 30 天的按日数据里截取（7 个完整 UTC 日加今天，和 Ollama 自己的 `7d` 区间一致），省掉一次请求。

需要注意：

- **余额和用量都是账户级的**：同一账户（或同一团队）的所有 Key 查到的数字相同。Ollama 不返回账户 ID，管理页把余额和 30 天用量完全相同的 Key 视为同一个账户，汇总时只算一次。
- **用量金额是请求的价值**，包括套餐额度覆盖的部分和用购买额度支付的部分，不等于实际扣款。时间段里有老套餐请求时 Ollama 只给请求数，这时显示请求数而不是金额。
- **可用余额 = 套餐额度剩余 + 购买的额度。**
- **限流**：这两个接口每个用户每分钟共 10 次，同一账户的所有 Key 共享。插件对每个 Key 的完整查询结果缓存 60 秒；余额接口返回 401 / 403 / 429 时不再调用用量接口。同一账户的 Key 超过 3 个时，"刷新余额" 一次查全部可能碰到 429，等一分钟再刷新即可。

CPA 原生额度面板显示：

- **Included credits**：套餐额度剩余比例的进度条，重置时间是周期结束时间；
- **Plan limits**：老套餐的会话限额和每周限额进度条；
- 汇总数字：购买的额度，以及 24h / 7d / 30d 用量（没有金额时显示请求数）。

## 管理页

在 CPA 管理面板的插件菜单里点 **Ollama** 打开，页面地址是：

```text
/v0/resource/plugins/ollama/manage
```

- 从管理面板里打开时复用面板"记住密码"保存的管理密钥；直接打开时手动输入，只保存在页面内存里。
- **凭据管理** 页：
  - 顶部一条统计条：可用余额，以及近 24 小时、7 天、30 天用量。按账户去重，同一账户的多个 Key 只算一次；悬停可以看到套餐额度和购买额度各占多少。
  - Key 以卡片网格展示（宽屏一行三张，窄屏单列）。每张卡片从上到下依次是：名称（默认是认证文件名，不带 `.json`）、状态、打码后的 Key 和优先级；左边是模型、前缀、出口代理和成功 / 失败次数（2×2），右边是余额和 24h / 7d / 30d 用量（同样 2×2，卡片较窄时移到下方），详情（请求数、输入 / 缓存 / 输出 Token、30 天按模型拆分）在鼠标悬停提示里；下面是套餐额度（或老套餐的会话 / 每周限额）进度条，左边是重置时间，右边是剩余额度（颜色跟进度条一致）；再下面是备注和状态；底部是查询时间和操作按钮。
  - 卡片上可以编辑、启用/停用、删除（删除需要 4 秒内再点一次确认）；可以按名称、Key 或文件名搜索。
  - 打开页面时自动查询所有 Key 的余额（停用的也查），"刷新余额"重新查询全部。查询按出口排队：同一个出口（同一个代理，或同样直连）一次只查一个 Key，这个 Key 的余额和用量依次查完再轮到下一个；不同出口之间同时查。CPA 额度面板发来的查询、保存 Key 时的校验也一起排队。
- 添加和编辑在弹窗里进行：名称、出口代理（继承 CPA 全局代理 / 直连 / 代理列表里的代理 / 自定义地址）、模型前缀、优先级，以及"全部对话模型 / 只用选中的模型"，选模型时可以按 ID、模型家族或能力搜索，并对比上下文长度和参数量。添加 Key 时默认先调用 Ollama 余额接口校验 Key 是否有效（Ollama 暂时连不上或限流时可以勾选"跳过 Key 校验"）；名称留空时用认证文件名。
- **代理** 页：管理出口代理列表。
  - 可以添加、编辑、删除代理。名称留空时用 `主机:端口`；地址只显示隐藏了密码的样子，编辑时地址留空表示不改。
  - 改了某个代理的地址时，正在用它的 Key（`proxy_url` 和旧地址完全相同）会一起改成新地址。删除代理不影响 Key：用它的 Key 保留原地址，变成"自定义地址"。
  - 有 Key 用了列表里没有的代理时，可以点"导入 Key 里的代理"一次加进来。
  - 列表上面固定两行：继承 CPA 全局代理、直连，对应 Key 编辑弹窗里 CPA 内置的两个选项，位置不能改。
  - 列表里的代理可以按住名称左边的手柄拖动排序（鼠标和触屏都行），也可以聚焦手柄后按 ↑ ↓ 移动；顺序存进 `proxies.json`，Key 编辑弹窗的下拉框也按这个顺序列出。
  - 检测要手动点"检测"（单个）或"检测全部"，不会自动检测。检测时插件通过这个代理请求 `https://ipinfo.io/json`，失败再试 `https://api.ip.sb/geoip`，User-Agent 用 `curl/8.7.1`；每次检测新建连接，延迟包含连上代理的时间，单个服务超时 10 秒。
  - 出口列显示国旗和出口 IP，下面是国家和城市（国家名按页面语言显示）；另外两列是延迟和检测时间（本地时间，带日期）。还没检测过的显示"—"。
  - 每次检测的结果（包括失败）都会保存，下次打开页面直接显示上一次的结果。代理改了地址，旧结果作废；CPA 全局代理变了，"继承"那行的旧结果也不再显示。
  - 代理列表和检测结果存在 `<data-dir>/proxies.json`（默认 `plugins/ollama-data/proxies.json`，权限 0600，里面有代理的用户名密码）。
  - 国旗图标来自 [Twemoji](https://github.com/jdecked/twemoji)（CC-BY 4.0），编进插件里，由 `/v0/resource/plugins/ollama/flag.svg?cc=<国家代码>` 提供，不访问外部网站。
- **模型目录** 页：Ollama 云端模型的模型家族、能力（视觉 / 思考 / 工具 / 向量）、上下文长度、参数量和量化方式、思考级别及默认值，以及每个模型在 CPA 里注册的名字和服务它的 Key 数。
- **请求日志** 页：经过本插件的每次调用，最新的在前。
  - 最上面是时间范围：全部、今天、24 小时、近 7 天、近 30 天、自定义（精确到分钟的起止时间，点"应用"生效）。按请求开始时间筛选，选择会记在浏览器里。
  - 统计条：请求数（成功 / 失败）、输入 / 输出 / 缓存 Token、缓存命中率、平均输出速度，按当前时间范围和筛选条件统计。
  - 每行显示时间、模型（和发给 Ollama 的上游模型）、思考强度、结果状态码、凭据（认证文件名，不带 .json）、Ollama 实际返回的模型、首字时间 / 总耗时、Token 用量；点开一行能看到 CPA 的请求 ID、接口、是否流式、CPA 的 auth_index、打码的 Key、缓存写入、思考 Token、输出速度，失败的还有错误码、出错阶段（输出前 / 流式中途）和错误信息。
  - 可以按请求 ID、模型、凭据、认证文件、错误信息或状态码搜索，按成功 / 失败筛选；每页 20 / 50 / 100 / 200 条，可以输入页码跳转；清空需要 3 秒内再点一次确认。
  - 日志只记录元数据和用量，不记录请求和响应内容。
- **配置概览** 页：插件配置的当前生效值（可以展开成 YAML）、CPA 的强制前缀开关和全局代理。强制前缀没开时会提示同名模型会进入公共号池。
- 支持 English / 中文。

请求日志存在 SQLite 数据库 `<data-dir>/ollama.db`（默认 `plugins/ollama-data/ollama.db`，WAL 模式，旁边会有 `-wal` 和 `-shm` 文件），查询、筛选和统计都在数据库里做。超过 `log-retention` 条或早于 `log-retention-days` 天的记录会自动删掉。SQLite 用的是 [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3)，随插件一起编译进去，不依赖系统的 libsqlite3。

详情里显示完整的请求 ID。CPA 日志和请求日志文件名里用的是它的末 8 位，把这 8 位粘贴进搜索框也能找到对应的记录。CPA 只把请求 ID 交给请求拦截器，所以插件注册了一个拦截器：CPA 选中 Ollama 凭据后，拦截器把请求 ID 放进内部请求头 `X-Ollama-Cpa-Request-Id`，执行器读出来记进日志。这个头不会转发给 Ollama；其他渠道的请求，拦截器不做任何修改。拦截器只读请求头和元数据，不接收请求体，需要 CPA 支持插件协议 schema 7；CPA 版本更老时不注册拦截器，日志里也就没有请求 ID。

启用、停用和删除走 CPA 原生的认证文件接口（`PATCH /v0/management/auth-files/status`、`DELETE /v0/management/auth-files?name=...`），和在 CPA 面板里操作效果相同。

## 管理接口

```text
GET   /v0/resource/plugins/ollama/manage             管理页外壳，无需鉴权，不含数据
GET   /v0/resource/plugins/ollama/logo.svg           插件 logo，无需鉴权
GET   /v0/resource/plugins/ollama/flag.svg?cc=hk     国旗图标，无需鉴权
GET   /v0/management/plugins/ollama/status           插件状态、目录摘要、Key 列表
GET   /v0/management/plugins/ollama/models           模型目录（能力、上下文长度、参数量、思考级别）
POST  /v0/management/plugins/ollama/models/refresh   强制重新拉取模型目录和详情
POST  /v0/management/plugins/ollama/credentials      添加 Key
PATCH /v0/management/plugins/ollama/credentials      编辑 Key
GET   /v0/management/plugins/ollama/credentials/quota?auth_index=...   Key 所属账户的余额，以及 24h / 7d / 30d 用量
GET   /v0/management/plugins/ollama/logs?search=&status=all|success|error&from=&to=&limit=50&offset=0   请求日志（最新的在前）和统计；from / to 是 RFC 3339 时间，按请求开始时间筛选，含 from 不含 to
DELETE /v0/management/plugins/ollama/logs            清空请求日志
GET   /v0/management/plugins/ollama/proxies          代理列表（地址隐藏密码、上一次检测结果）和 CPA 全局代理
POST  /v0/management/plugins/ollama/proxies          添加代理 {"name","url"}
PATCH /v0/management/plugins/ollama/proxies          修改代理 {"id","name","url"}，改地址时同步改用它的 Key
DELETE /v0/management/plugins/ollama/proxies?id=...  删除代理，用它的 Key 保留原地址
POST  /v0/management/plugins/ollama/proxies/import   把 Key 里用到、列表里还没有的代理加进列表
POST  /v0/management/plugins/ollama/proxies/order    调整代理顺序 {"ids":[...]}
POST  /v0/management/plugins/ollama/proxies/check    检测出口 IP、国家、城市和延迟并保存结果，{"id"} 或 {"target":"direct"|"inherit"}
```

`/v0/management` 路由由 CPA 宿主先校验管理密钥，通过后才转发给插件。

```bash
# 添加 Key，只注册两个模型，不加前缀
curl -X POST -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  -d '{"api_key":"<Ollama API Key>","models":["gpt-oss:120b","qwen3-coder:480b"],"prefix":""}' \
  http://127.0.0.1:8317/v0/management/plugins/ollama/credentials

# 改成注册全部对话模型、使用默认前缀、走指定代理
curl -X PATCH -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  -d '{"auth_index":"<AUTH_INDEX>","models":null,"prefix":null,"proxy_url":"socks5://127.0.0.1:1080"}' \
  http://127.0.0.1:8317/v0/management/plugins/ollama/credentials
```

添加 Key 时加 `"skip_verify": true` 可以跳过余额接口校验。添加和编辑 Key 时可以用 `"proxy_id"` 代替 `"proxy_url"`，表示使用代理列表里的那个代理（写入它的地址）。Key 的 `proxy_url` 和列表里某个代理完全相同时，`status` 返回的这个 Key 带 `proxy_id` 和 `proxy_name`。

编辑接口只修改请求里出现的字段：`null` 表示恢复默认（`models` 恢复为全部对话模型，`prefix` 恢复为插件默认前缀，`priority` 清除），空字符串表示清除（`label`、`note`、`proxy_url`、`base_url`）。`api_key` 不能修改，换 Key 请新增一个再删掉旧的。

## 安全说明

- 读取和修改 Key 的接口都需要 CPA 管理密钥；公开的只有不含数据的页面外壳、logo 和国旗图标。
- 管理页设置了 `frame-ancestors 'self'`，其他站点不能嵌入。
- 接口和页面只显示打码后的 Key 和隐藏了密码的代理地址。
- 模型目录接口（`/api/tags`、`/api/show`）不需要鉴权，插件拉取目录时不发送任何 API Key。

## 本地构建

管理页在 `web/`，用 React + TypeScript + Vite 写，构建成单个 HTML 文件后嵌进插件。需要 Node 20 和 Go：

```bash
make web        # 构建管理页，输出到 internal/web/dist/index.html
CGO_ENABLED=1 go build -buildmode=c-shared \
  -ldflags '-X main.pluginVersion=0.1.0-dev' \
  -o ollama.so .
```

macOS 用 `.dylib`，Windows 用 `.dll`。c-shared 构建会顺带生成一个 C 头文件，宿主用不到它。没执行 `make web` 就构建的话，管理页只是一个提示"尚未构建"的占位页。

本地调试管理页可以在 `web/` 里运行 `npm run dev`，开发服务器会把管理接口代理到 `VITE_CPA_BASE`（默认 `http://127.0.0.1:8317`）。

推送 `v*` tag 后，GitHub Actions 会先构建管理页，再跑 gofmt / vet，然后构建五个平台的产物，按插件商店规范打包成 `ollama_<version>_<goos>_<goarch>.zip` 并连同 `checksums.txt` 发布 Release。仓库目前不带单元测试。

## License

MIT
