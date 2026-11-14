# CPA Ollama Plugin

CLIProxyAPI（CPA）插件：把 [Ollama Cloud](https://ollama.com) 的 API Key 作为 CPA 认证文件接入，转发 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages。

- 每个 Key 是一个认证文件，和其他凭证一样参与 CPA 的选号、冷却和故障转移，可以单独设出口代理
- 自动拉取 Ollama 云端模型目录注册到 CPA（带上下文长度和视觉 / 工具 / 思考能力），默认模型名 `ollama/<模型 ID>`，也可以只注册选中的模型
- 请求原样转发到 `https://ollama.com/v1/...`，不做协议转换
- 查看账户的套餐额度、购买额度和近 24 小时 / 7 天 / 30 天用量，CPA 额度面板也能看
- 自带管理页：Key、模型、出口代理、请求日志

> 社区插件，与 Ollama 官方无关。

## 安装

需要 CLIProxyAPI v8 并开启动态插件（Unix 上 CPA 要用 cgo 编译）；支持 Linux / macOS（amd64、arm64）和 Windows amd64。API Key 在 [ollama.com/settings/keys](https://ollama.com/settings/keys) 创建。

**插件商店**：在 CPA 配置里加上商店源，然后在管理面板的插件商店里安装 **Ollama**。

```yaml
plugins:
  enabled: true
  store-sources:
    - https://raw.githubusercontent.com/berry-shake/CLIProxyAPI-Plugins-Store/main/registry.json
```

**手动安装**：从 [Release](https://github.com/berry-shake/cpa-ollama-plugin/releases) 下载对应平台的 zip，把里面的 `ollama.so`（macOS 是 `.dylib`，Windows 是 `.dll`）放到 `plugins/<系统>/<架构>/` 下，再在配置里打开 `plugins.configs.ollama.enabled`。配置里已有 `store.version` 时，CPA 只加载版本号对得上的文件，手动换文件要同时改 `store.version` 和 `release-tag`。

## 配置

都是可选项，写在 `plugins.configs.ollama` 下；填错的值回退默认并记一条警告。

```yaml
plugins:
  configs:
    ollama:
      enabled: true
      base-url: https://ollama.com          # 末尾的 /v1 或 /api 会自动去掉
      model-prefix: ollama                  # 认证文件没写 prefix 时的默认前缀，"" = 不加
      models-refresh-minutes: 60            # 模型目录轮询间隔，0 = 不轮询
      request-timeout-seconds: 600          # 非流式请求超时
      stream-idle-timeout-seconds: 300      # 流式响应多久没数据就断开
      data-dir: plugins/ollama-data         # 请求日志和代理列表的目录
      log-retention: 100000                 # 请求日志最多保留条数，0 = 不记录
      log-retention-days: 30                # 请求日志保留天数，0 = 只按条数清理
```

## 认证文件

`type` 为 `ollama` 的认证文件归本插件管理；管理页添加的 Key 保存为 `ollama-<指纹>.json`。

```json
{
  "type": "ollama",
  "api_key": "<Ollama API Key>",
  "label": "主账号",
  "models": ["gpt-oss:120b", "qwen3-coder:480b"],
  "prefix": "ollama",
  "proxy_url": "socks5://127.0.0.1:1080"
}
```

| 字段 | 说明 |
|---|---|
| `api_key` | 必填 |
| `label` | 显示名称，默认用文件名 |
| `models` | 要注册的模型。不写或 `null` = 目录里全部对话模型，`[]` = 不注册 |
| `prefix` | 模型名前缀。不写用配置里的 `model-prefix`，`""` = 不加前缀 |
| `proxy_url` | 出口代理，支持 `http(s)://`、`socks5(h)://`，`direct` = 强制直连；不写时依次用 CPA 的 `proxy-url`、环境变量 |
| `base_url` | 覆盖配置里的 `base-url` |
| `priority` / `note` / `disabled` | CPA 通用字段 |

`model_revision` 由插件维护，不要手改。

> CPA 的 `routing.force-model-prefix` 默认关闭，这时模型会同时注册成 `ollama/gpt-oss:120b` 和 `gpt-oss:120b`，后者会和其他渠道的同名模型混在一起轮询。只想用带前缀的名字就把它打开（全局生效）。

## 工作方式

**模型**：每隔 `models-refresh-minutes` 拉一次 `/api/tags`，新出现或 digest 变了的模型再用 `/api/show` 读上下文长度、能力和思考级别。「全部模型」只注册对话模型；模型或能力有变化才重新注册；拉取失败保留上一次的目录。这两个接口不需要鉴权，插件不会发出 API Key。

**转发**：

| 客户端格式 | Ollama 接口 |
|---|---|
| OpenAI Chat Completions | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| Anthropic Messages | `POST /v1/messages` |

- 只改写请求体里的 `model`，其余原样转发；Chat 流式请求会自动加上 `stream_options.include_usage`
- Ollama 的 Responses 只支持无状态调用，`previous_response_id` 之类的字段由 Ollama 自己报错
- Ollama 没有 `count_tokens`，这类请求直接返回 501
- 流式响应在第一帧真正的输出之前，上游报错（限流、过载）按普通错误处理，CPA 照常冷却或换 Key；流没收到结束帧就断了，记为 `stream_truncated`（502）

**上游错误**：

| Ollama 返回 | 交给 CPA | 效果 |
|---|---|---|
| 401 / 402 / 403 | 原状态码 | 冷却这个 Key |
| 404（模型不存在） | 400 | 只失败这一次 |
| 429（限流或套餐用量到顶） | 429，带 `Retry-After` | 按上游给的时间冷却 |
| 502（云端模型暂时不可用）/ 529 / 499 | 502 / 503 / 408 | 可重试 |

## 余额和用量

查一个 Key 时，插件调用 Ollama 的[余额](https://docs.ollama.com/api/balance)和[用量](https://docs.ollama.com/api/cloud-usage)接口：

- **可用余额** = 套餐额度剩余 + 购买的额度；老套餐显示会话限额和每周限额的剩余百分比
- **用量**：近 24 小时 / 7 天 / 30 天的金额和按模型拆分。金额是请求的价值，含套餐额度覆盖的部分，不等于实际扣款
- 余额和用量都是账户级的，同一账户的 Key 数字相同，汇总时只算一次
- 这两个接口每个账户每分钟共 10 次，插件缓存 60 秒；同一账户超过 3 个 Key 时，一次刷新全部可能碰到 429，等一分钟再刷

CPA 额度面板显示 **Ollama** 一组进度条：套餐额度剩余（`monthly`），老套餐是 `session` 和 `7d`。

查询按出口排队：同一个出口一次只查一个 Key，不同出口同时查。

## 管理页

在 CPA 管理面板的插件菜单里点 **Ollama**（`/v0/resource/plugins/ollama/manage`）。

- **凭据**：Key 卡片，添加、编辑、启停、删除，选模型，看余额和用量
- **代理**：常用出口代理列表，编辑 Key 时直接选；可以检测出口 IP、地区和延迟
- **模型目录**：模型家族、能力、上下文、参数量和思考级别，以及在 CPA 里注册的名字
- **请求日志**：每次调用的模型、状态、首字时间、耗时、Token 和速度，可以用 CPA 日志里的 8 位请求 ID 搜索；只记元数据，不记内容
- **配置概览**：当前生效的配置

请求日志里有两种速度，统计条取成功请求的中位数：

- **生成速度** =（输出 Token − 1）÷（总耗时 − 首字时间），只算流式请求，即 vLLM 的 1 ÷ TPOT
- **端到端速度** = 输出 Token ÷ 总耗时，包含等首字的时间，即 Artificial Analysis 的 Output Speed

数据都在 `data-dir` 下：`ollama.db`（请求日志，SQLite）和 `proxies.json`（代理列表，含代理密码，权限 0600）。

## 管理接口

都在 `/v0/management/plugins/ollama/` 下，CPA 先校验管理密钥再转给插件。

| 接口 | 用途 |
|---|---|
| `GET status` | 插件状态和 Key 列表 |
| `GET models`、`POST models/refresh` | 模型目录、强制重新拉取 |
| `POST credentials`、`PATCH credentials` | 添加、编辑 Key |
| `GET credentials/quota?auth_index=` | 账户余额和用量 |
| `GET logs`、`DELETE logs` | 查询、清空请求日志 |
| `GET` / `POST` / `PATCH` / `DELETE proxies`，`POST proxies/import`、`proxies/order`、`proxies/check` | 代理列表 |

```bash
curl -X POST -H "Authorization: Bearer $MANAGEMENT_KEY" -H "Content-Type: application/json" \
  -d '{"api_key":"<Ollama API Key>","models":["gpt-oss:120b"],"prefix":""}' \
  http://127.0.0.1:8317/v0/management/plugins/ollama/credentials
```

编辑只改传了的字段：`null` 恢复默认，`""` 清空。`api_key` 不能改，换 Key 就新增一个再删掉旧的。启停和删除用 CPA 原生的认证文件接口。

## 构建

需要 Go 和 Node 20：

```bash
make web    # 构建管理页（web/，React + Vite），嵌进插件
CGO_ENABLED=1 go build -buildmode=c-shared -o ollama.so .
make web-test && go test ./...
```

推送 `v*` tag 后，GitHub Actions 跑测试、构建五个平台并发布 Release。

## License

MIT。国旗图标来自 [Twemoji](https://github.com/jdecked/twemoji)（CC-BY 4.0）。
