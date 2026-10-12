# 运行、更新与排障

## 服务边界

浏览器入口 `127.0.0.1:18080` 由独立 Go 服务提供。原面板仅监听 `127.0.0.1:12053`，Xray API 仅监听 loopback `62789`。本地验收默认关闭订阅服务。没有安装系统服务；WSL 重启后运行 `scripts/start.sh`。

`runtime/monitor/config.json` 中统一设置地址、日志路径、采样周期和保留期。`detail_days` 范围 1～7，`summary_days` 范围为明细天数～35；首版默认 7/35。修改配置后仅重启监测服务。

采集 Token 只在服务端读取，官方 3.9.0 的 monitor scope 不允许读取客户端列表，因此使用专用 admin Token。应用不会提供通用代请求 API；浏览器会话校验不使用该 Token。

当前页面统一显示所选范围的合计流量，清理通过“删除日志与流量”按分钟和客户端执行。实时在线列表复用 10 秒采样并读取官方缓存在线名单；后台采集模块只读取固定接口。详细口径见 `targets-and-deletion.md`。

## 更新官方面板

当前 WSL 使用项目管理的发行包目录，不使用全局安装脚本。以下操作下载官方稳定 tag 并验证官方 SHA256，再替换本项目官方程序，保留 `data/panel`、`data/monitor` 及独立监测程序：

```bash
source scripts/env.sh
python3 scripts/upgrade-panel.py v3.9.0
python3 scripts/verify-runtime.py
```

把参数换成已经验证的目标稳定版本。升级失败时脚本尝试恢复前一版程序；官方数据库升级可能改变 schema，跨版本降级不保证兼容。因此实际服务器升级前应另行执行数据库备份与恢复演练。当前测试环境不含需要迁移的旧部署数据。

不要把官方安装目录改到监测目录。实际 Linux 服务器可以使用官方正常更新机制，只需保持反代入口、独立程序/数据目录，以及面板持久化 Xray 模板中的 access 路径。面板发生较大布局变化时，更新 `web/public/adapter.js` 及其回归测试后重新构建。

升级后检查：官方页面与登录正常；监测页面可读历史；侧栏入口与顶部保留；后台采样恢复；access log 仍持续写入。如果日志路径被改变，先核对新配置，再运行 `python3 scripts/lab.py configure-log`，它只修改日志字段和补充 LoggerService，不恢复旧整份路由模板。

官方 v3.9.0 会将 Xray 日志路径约束到 `XUI_LOG_FOLDER` 下，因此本项目将该变量设为 `runtime/logs`，确保持久模板、实际运行配置和采集路径三者一致。实际服务器接入时必须同时核对这三者。

## 故障定位

| 现象 | 检查 |
|---|---|
| 浏览器无法打开 | `python3 scripts/lab.py status`，然后查看 `runtime/logs/monitor.log`、`panel.log` |
| Failed to fetch dynamically imported module / Unexpected Application Error | 旧页面先 Ctrl+F5，加载当前恢复脚本。之后若资源加载失败，使用“重新加载页面”；需重新打开之前的弹窗。检查上游是否重启、资源是否 404 或被浏览器扩展阻止，详见下节 |
| 提示会话过期 | 从原面板重新登录；独立页面与 Tab 都使用同一 Cookie |
| 合计/实时流量不刷新 | 采集 Token 是否失效、原面板是否运行、客户端列表接口是否可用 |
| 今天没有访问明细 | 先看最近访问提示并跳转对应日期，再检查访问是否真的经过当前监测的入站、入站是否启用、Xray access 路径及日志是否写入 |
| 手机在用远程代理，但本机没有新记录 | 本机 WSL 只读取本机面板；分享链接没有远程日志读取能力，需在实际服务器接入监测 |
| 只有 IP | 开启服务器嗅探不保证 access log 改写目标；优先让客户端传递域名，或在客户端使用适合其模式的嗅探/DNS 映射。未知 IP 保留原值 |
| 服务名称未匹配或需要更细名称 | 按 `docs/targets-and-deletion.md` 检查原域名、规则来源与本地覆盖，再生成规则并重新构建监测程序 |
| 删除后原面板累计还有数值 | 删除清理的是监测历史；官方累计和客户端配额独立管理 |
| 删除预览失效 | 当前范围新数据导致记录指纹变化、会话 Cookie 变化、超过 5 分钟或服务重启，需要重新预览 |
| 侧栏不兼容 | 使用 `/traffic-monitor/` 独立地址，核对实际官方版本并更新适配器 |
| 用量出现缺口 | 核对面板流量重置、手动改值、面板停机或核心/日志异常；不人工补造数据 |

日志轮转由监测服务完成，默认达到 64 MiB 或一天后轮转；通过本机 Xray LoggerService 重开日志。正常运行时数据库每分钟执行到期清理，查询仍按保留期过滤，重启会先清理过期数据。关闭服务期间不承诺定时清理，恢复启动后补做。

监测服务也识别并清理 `.1` 等数字后缀和 `.2026-10-01` 等日期后缀的未压缩轮转日志，仅处理同目录内严格匹配的普通文件；不处理符号链接、其他文件或 `.gz` 压缩包。本项目无需额外 logrotate。若实际服务器另用压缩轮转，压缩包保留期由该外部程序配置。原始日志按最早记录过期后整文件删除，可能比数据库完整 7 天窗口少不到一天。

本项目数据放在 WSL `/mnt/d` 挂载盘，SQLite 使用 DELETE 回滚日志、单连接与串行写入，避免依赖跨平台 WAL 共享内存。真实服务器应同样先验证存储和权限，再迁移。

### 官方页面分包加载与恢复

原面板按需加载客户端二维码、编辑弹窗及不同页面。2026-10-10 检查时，报错的 `ClientQrModal-Bt-NPyL_.js` 与相关依赖共 27 个文件均存在，直接上游和代理返回正常；没有证据确认用户当时第一次请求失败的具体网络原因。故障注入已经复现：中断该请求，且官方的单次自动刷新额度已在当前会话使用过，原版就停在 `Unexpected Application Error`。

现在代理只对需要注入的 HTML 去除压缩与缓存校验；JS/CSS、API、下载保留官方原有编码和缓存行为。JS/CSS 的 GET/HEAD 遇到网络错误或 502/503/504 时最多额外重试两次，等待 150/300ms；不重试写操作、API、404 或认证拒绝，不缓存错误响应，不改写官方安全响应头。

官方 assets 中间件也会为缺失资源的 404 设置一年缓存头。代理现在对静态资源错误响应改为 no-store 并去掉缓存校验头，避免失败被浏览器长期记住；成功资源的原始缓存仍保留。此前已经缓存的失败需 Ctrl+F5 绕过旧缓存后更新。

独立 `panel-recovery.js` 在官方模块入口前加载，保留官方的单次自动恢复逻辑；已经用尽自动恢复时提供“重新加载页面”和独立监测链接，明确提示未保存编辑会丢失。没有无限刷新或自动重复提交写请求。布局适配器与恢复脚本使用 no-cache，面板未知版本仍能加载恢复控制而不强行嵌入 Tab。已打开的旧错误页需要手动刷新一次才能取得新脚本。

复现/验收脚本：`node scripts/browser-tests/panel-recovery.mjs` 仅在该测试浏览器拦截一次模块请求，验证提示及重新加载后的二维码弹窗；`python3 scripts/test-official-functions.py` 为二维码临时创建一个回环入站/客户端，验证完成后只删除该临时对象，比较已有客户端和入站确保未更改。设置、编辑弹窗仅打开后关闭；外部节点、机器人和邮件是否可用仍取决于实际配置。

## 验证命令

```bash
source scripts/env.sh
(cd apps/traffic-monitor && go test ./cmd/... ./internal/...)
(cd apps/traffic-monitor/web && npm test && npm run build)
python3 scripts/verify-runtime.py
python3 scripts/test-real-traffic.py
python3 scripts/verify-real-traffic.py
python3 scripts/test-live-rotation.py
node scripts/browser-tests/verify.mjs
node scripts/browser-tests/panel-recovery.mjs
python3 scripts/test-official-functions.py
python3 scripts/verify-panel-assets.py
python3 scripts/test-enhancements.py
python3 scripts/test-realtime.py
# 需要事先存在 runtime/monitor/private/user-reality-outbound.json 私有出口配置：
python3 scripts/test-reality-traffic.py
python3 scripts/test-official-upgrade.py
```

真实流量验收只创建本地回环 VLESS 入站和两个测试客户端，临时路由在 finally 中恢复，测试结束禁用该入站。验收过程中不要并行修改 Xray 模板。

REALITY 验收另使用 `monitor-reality-check`、回环 15444 和临时 SOCKS 11082，链路是本地 REALITY 入站经用户授权的私有远程出口访问固定网站首页。脚本还对比传域名、传 IP、客户端嗅探三种方式；服务端开启嗅探也不保证访问日志记录域名。原模板与私有连接配置仅存于 `runtime/monitor/private`，报告不含连接凭证；finally 恢复模板并禁用测试入站。

实时验收使用 `monitor-live-check`、回环入站 15446、SOCKS 11083 及 HTTP 19093，持续上传 4 MiB、下载 8 MiB，验证在线状态、上下行速度、停止后的离线状态和浏览器布局。临时路由在 finally 中恢复，测试入站结束后停用。不会修改其他客户端计数。旧累计清零 UI 验收脚本已停用；保留的兼容 API 由 Go 测试覆盖。

新增目标识别/删除的浏览器测试使用 18082 端口和 `artifacts/fixtures` 内的隔离数据库，通过真实官方会话验证。仅删除隔离数据库的测试历史，结束后关闭该监测实例。

App/网页统一归类与分钟范围操作见 `targets-and-deletion.md`。规则来自固定版本的 domain-list-community、经审阅的 ios_rule_script 域名和 Telegram 官方专属 CIDR；完整规则与许可证随源码保留。`service-names.json` 仅是显示别名，完整可识别服务目录为 `rules/service-catalog.json`，所有匹配域名在 `rules/domains.json`。

1C/1G/20GB 的实测与容量估算见 `resource-assessment.md`。`log_backlog` 表示文件达到每轮采集限额、近期连接记录尚未完整同步，后续轮次会继续追赶。页面每 30 秒查询当前历史记录表，实时列表约每 10 秒读取内存快照；后台仍每 10 秒采集。点击暂停自动刷新只暂停页面查询。可用 `bash scripts/profile-resources.sh` 创建隔离压力数据、`python3 scripts/profile-idle.py` 只读测量当前项目的三项后台进程。

完整 35 天容量与并发查询使用 `bash scripts/profile-peak.sh`；五个独立客户端的本机 VLESS/TCP/REALITY 转发测试使用 `python3 scripts/profile-reality.py`。两者均不操作现有业务历史，REALITY 压力流量限于回环。最新容量已修正旧测试缺少完整访问次数桶、身份主键偏短的问题，最终 10 万访问/天这一档数据库为 290 MiB；整周查询仍可能让采集等约 21 秒，日常建议使用今天或最近一小时范围。

## 停止和移除

先运行 `scripts/stop.sh`。本项目未新增系统服务或全局命令，移除该项目目录即移除其代码、工具、缓存和数据。需要保留历史时，先独立保存 `data` 与必要配置。旧环境清理脚本 `scripts/cleanup-legacy.sh` 专门针对已核验的旧路径，不作为新项目卸载脚本。
