# 验证矩阵

本文件只记录实际测试证据。未执行、待运行和自动化覆盖设计不代表验收通过。所有测试文件、缓存和报告保存在项目根目录。

## 旧环境清理

**已通过，2026-10-08。** 旧服务及别名不存在，旧进程退出，2053/2096/11111/62789 释放，固定清理清单中的目录和入口不存在，旧专用测试容器及镜像被移除。九个无关 Suricata 容器保持不变。详见 `environment-check.md`。

## 运行时验证

**已通过，2026-10-09 09:57（Asia/Shanghai），10 项通过、0 项失败。** 实际报告：`artifacts/reports/runtime-checks.json`，该次采集状态为 `ok`，无健康告警。

脚本：`scripts/verify-runtime.py`。在 WSL 中、面板和监测服务运行后执行：

```bash
cd /mnt/d/work/codexworkspace/x-ui-tab
source scripts/env.sh
python3 scripts/verify-runtime.py
```

脚本读取 `runtime/lab-state.json`，不会输出账号口令、Cookie或后台 Token；不修改面板配置、真实监测数据或日志。它创建自己的登录会话，并在 `artifacts/reports/` 内创建 SQLite 测试数据库，输出 `artifacts/reports/runtime-checks.json`。

| 检查 | 当前证据 |
| --- | --- |
| 无 Cookie、伪造 Bearer、真实后台 Token 都不能读取监测数据 | 五种组合全部 HTTP 401，包括真实 Token 搭配无效 Cookie；匿名页面返回登录 |
| CSRF 登录，原面板和监测页共享浏览器会话 | 同一 Cookie 会话成功访问双方 |
| clients/summary/traffic/visits/health 契约与 7/35 配置 | 五个接口通过；配置为 7/35，timezone=Asia/Shanghai |
| 无效日期、分页及入站拒绝；查询接口写入方法拒绝 | 五项参数检查返回 HTTP 400，向查询接口 POST 返回 405；专用删除接口另行验证 |
| 超过保留期明确标记过期 | 历史窗口同时返回 detail_expired、summary_expired |
| 页面禁止缓存、同源嵌入、安全头保留及秘密不在页面中 | no-store、SAMEORIGIN 和原安全头检查通过 |
| 持久 Xray 模板与运行配置的 access 路径一致，LoggerService 启用 | 只读核对通过 |
| 过期浏览器 Cookie 和伪造会话拒绝 | 均返回 HTTP 401 |
| 退出后监测访问立即拒绝 | HTTP 401 |
| 实际监测数据库 DELETE 日志及 integrity_check | delete / ok，只读打开 |
| 当前挂载盘并发读写、进程异常退出后的 SQLite 事务恢复 | 100 次提交、两读线程 61/64 次读取；512 行未提交事务异常退出后完整回滚 |

过期浏览器 Cookie 测试只模拟客户端停止发送已过期 Cookie，不修改实际服务器时钟或会话寿命。挂载盘测试使用 Python sqlite3 验证文件系统与 SQLite 锁/恢复行为，应用 Go 驱动另由后端测试验证。

## 其他验收证据

以下结论来自已实际生成并核对的报告。

| 领域 | 需要的证据 |
| --- | --- |
| 后端数据 | `backend-tests.txt` 已通过：采样基线、第一分钟、7/35 保留、身份改名重用、计数重置、跨日断采、事务与重启持久化 |
| 日志可靠性 | 同一 Go 报告通过：IPv6、异常行、半行、重启去重、轮转、截断、自定义入站映射及内部 API 排除 |
| 会话隔离 | Go 报告通过：`/secret/` 非根路径、拒绝伪成功/跳转、Cookie与Token隔离、上游暂不可用返回503、未知升级版本检测 |
| 真实流量 | `real-traffic.json` 和 `real-traffic-monitor.json` 通过：两个 VLESS 客户端各四条 HTTP/HTTPS/IP/上传记录，身份正确，采样上下行与官方累计一致 |
| 前端单元测试 | 10 项通过：北京时间日期边界、35 天范围、历史客户端计数、侧栏与嵌入、非根路径、幂等、未知版本及布局 |
| 界面 | 最终 `browser-checks.json` 十项通过：官方侧栏/顶部与嵌入、目标/客户端/指定日期筛选、深浅主题、固定侧栏、前进后退及刷新、手机抽屉、未知版本回退、独立页面、退出。最终报告 pageErrors 和 consoleErrors 均为空 |
| 实际 Xray 日志重开 | `live-log-rotation.json` 通过：真实 LoggerService 调用后文件 inode 改变，新文件继续收到 Xray 输出，原 11 条客户端记录未重复或丢失 |
| 同版本发行包替换 | `release-preservation.json` 通过：官方3.9.0包替换后监测进程、程序/配置/资源/Token保持，11条历史访问和5个非零流量桶保留，采集恢复 |
| 跨版本官方升级 | `version-upgrade.json` 9项通过：真实3.8.5→3.9.0、同一隔离面板数据库迁移、客户端稳定身份/配置、完整Xray模板、非根路径及其他配置保留，数据库完整性正常 |

跨版本测试由 `scripts/test-official-upgrade.py` 执行，所有发行包重新验证 SHA256；使用 `/upgrade-test/`、面板12055、Xray API62795和指标11115，与主部署隔离。此测试的监测历史保留使用单独 SQLite 哨兵文件和配置 SHA256 验证，并未运行第二个实时监测进程。真实运行中监测历史保留由同版本发行包替换测试补充验证。测试进程已停止，专用端口释放。

后端最终共 **18 个命名测试通过**（`backend-tests.txt`，7.100 秒），包含 gRPC `RestartLogger` 方法连通、外部数字/日期日志到期清理和已过期活动日志立即重开。相同最终生产代码的竞态检查通过（`backend-race.txt`，9.040 秒）。`go vet` 退出码为 0；`backend-vet.txt` 为空是该命令成功时没有诊断输出。

## 代码审查跟进

- 启用后首个分钟桶的查询裁剪问题已修复，`TestFirstMinuteBaselineResetAndRetention` 实际通过。
- 兼容性最初仅依赖静态版本配置的问题已修复：读取官方状态和页面版本，相关未知版本回归通过。

## 2026-10-09 域名规则、目标汇总与删除扩展

| 检查 | 证据 |
| --- | --- |
| 百度、GitHub、YouTube、抖音规则；后缀边界与 exact 主机规则；IP 回退 | `backend-enhancements.txt` 中 `TestDomainRulesServicesBoundariesAndIPFallback` |
| 相同客户端 localhost 不同端口合并；服务名筛选；分页及 7/35 保留口径 | `TestTargetsGroupPortsClientsAndFilterByService` |
| 大量子域名的元数据限量；不同未匹配 email 的独立分组 | `TestTargetsBoundedMetadataAndUnmatchedIdentities`：两身份各 100 个域名，分页后每组最多返回 50 个地址/端口，访问次数和总地址数均保留 100 |
| 删除范围、访问数量与采样字节同时更新；其他客户端不受影响 | `TestDeleteHistoryIsolatedAtomicAndPanelBaseline`，并注入 SQL 失败验证完整事务回滚 |
| 预览后的同数量记录变更也必须重新确认 | `TestDeletePreviewRejectsChangesAndNarrowFilters`，修改目标但保持行数后返回 ErrDeletionChanged |
| 重启、复制日志重读、继续采样不会恢复已删除历史 | `TestDeletionSurvivesRestartReplayAndSampling` |
| 第 8～35 天删除无明细的历史仍能同步清除次数与用量 | `TestDeleteExpiredDetailsStillRemoves35DayAggregates` |
| 当前会话、同源、CSRF、一次性、过期预览及并发变更 | `deletion_server_test.go` 两项接口测试 |
| 前端构建和类型检查 | `frontend-enhancements-build.txt`；Vite 成功，包含组件库体积提示 |
| 实际 UI 服务名、合并、搜索、预览、确认删除、移动端与主题 | `enhancement-browser-checks.json`；使用隔离数据库和真实官方会话 |

扩展后的后端共 **27 个命名测试**，竞态测试与 vet 结果分别在 `backend-enhancements-race.txt`、`backend-enhancements-vet.txt`。删除流程使用 `artifacts/fixtures` 中单独的监测实例，实际主实例数据库的 11 条访问明细保留，`history_deletions` 为 0，SQLite integrity_check 为 ok。

目标元数据查询经过优化：整个结果页共用一次去重扫描，避免按每个分组和列反复扫描客户端一周日志。每个展示字段最多保存/返回 50 个不同值。测试还覆盖浏览器减少动态效果设置；Ant Design 的 motion 配置随系统偏好关闭，避免全局 CSS 禁用动画后弹窗和下拉框停在未完成状态。

最终新增功能浏览器 **6 项通过**，pageErrors/consoleErrors 均为 0；原面板嵌入回归 **10 项通过**，pageErrors 为 0。原面板在测试执行页面跳转/刷新时记录了 4 条 fetch 失败控制台信息，对应请求失败记录均为 `net::ERR_ABORTED`，由原版 `QueryProvider`/`IndexPage` 输出，保留在 `browser-checks.json` 供核对。运行环境检查 **10 项通过、0 失败**，包含原登录复用、退出、秘密不进入页面、7/35 保留配置及实际 SQLite 完整性。

## 2026-10-09 App 统一归类、分钟筛选和资源测量（当前版本）

当前后端 **31 个常规命名测试通过**（`backend-apps-minutes.txt`）；`backend-apps-minutes-race.txt` 竞态测试通过，`backend-apps-minutes-vet.txt` 没有诊断。资源测试显式启用后单独运行，常规套件跳过，不混入常规测试计数。

| 检查 | 当前证据 |
| --- | --- |
| YouTube、Telegram、X、OpenAI、淘宝等网页/App/API/媒体统一；Telegram IPv4/IPv6，网段边界与共享服务排除 | `TestAppWebsiteMediaAndExclusiveNetworksShareServices` |
| 09:30 单分钟的访问、目标合并、次数、流量、删除口径一致；相邻分钟完整保留；越界分页总数正确 | `TestMinuteBoundsApplyToDetailsTargetsTrafficAndDeletion` |
| 闲置客户端保持基线、不生成全零用量桶 | `TestIdleSamplingKeepsBaselineWithoutEmptyTrafficBuckets` |
| 日志超过 2,000 行时显示积压，下一轮追赶后清除提示；2,001 条无丢失/重复 | `TestLogBacklogVisibleUntilNextBatchCatchesUp` |
| 前端北京时间分钟、跨年与 35 天边界，适配器 | `frontend-apps-minutes-tests.txt`：10 项通过 |
| 类型检查与生产构建 | `frontend-apps-minutes-build.txt`：成功；主 JS gzip 约 364 kB，Vite 有较大包提示 |
| 新增功能真实浏览器流程 | `enhancement-browser-checks.json`：8 项通过，pageErrors/consoleErrors 均为空 |
| 原侧栏/顶部、主题、固定侧栏、日期、刷新/历史导航、手机、未知版本回退、登录退出 | 当前 `browser-checks.json`：10 项通过，pageErrors 为空 |
| 原会话复用、安全头/凭证排除、接口、保留规则、SQLite 锁及崩溃恢复 | 当前 `runtime-checks.json`：10 项通过、0 失败 |
| 单 CPU、70,000/700,000 访问记录及 252,000 用量桶 | `resource-single-scan.json`；`resource-single-scan-recent.json` 补充 1 小时/24 小时查询 |
| 实际面板、Xray、监测三进程 30 秒空闲资源 | `resource-idle.json`：三进程 RSS 合计约 196.7 MiB；测量不含 OS 或真实代理高峰 |
| 更新运行产物、测试未删除真实历史 | `apps-minutes-live-preservation.json`：运行程序与构建 SHA-256 相同，真实访问 11 条、删除范围 0 条、SQLite integrity_check 为 ok |

浏览器隔离样本是每客户端 25 条、10 个服务组。它验证同一客户端 Telegram 的域名、IPv4 和 IPv6 合并为一组，YouTube 网页/API/视频合并，OpenAI 与淘宝 App 子域名合并；同一开始/结束分钟生成精确 UTC 一分钟区间，前一分钟为空；删除弹窗也使用精确分钟范围，确认后仅所选客户端的访问、次数与 3 MiB 流量一起清除。

当前面板回归在快速导航/刷新过程中出现 3 条官方 `QueryProvider` 的 fetch 失败控制台信息，请求失败记录均为 `net::ERR_ABORTED`；监测筛选变更同样主动取消旧请求。没有未捕获页面异常，独立新增功能测试无控制台错误。原版消息保留在报告，不宣称原面板控制台完全无信息。

截图：`monitor-service-summary.png`（网页/App 合并样本）、`monitor-minute-filter.png`、`monitor-delete-preview.png`、`monitor-delete-mobile.png`；原面板嵌入截图已同步重新生成。资源条件、改进前后结果及尚未测试的真实 VPS 加密转发边界见 `resource-assessment.md`。

## 2026-10-10 REALITY 访问、空日期提示与面板累计清零

最终后端 **34 个命名测试通过**（`backend-reality-reset.txt`，20.130 秒），竞态检查通过（`backend-reality-reset-race.txt`，29.158 秒），`go vet` 无诊断。前端原有 10 项测试、TypeScript 检查及生产构建通过，报告为 `frontend-reality-reset-tests.txt` / `frontend-reality-reset-build.txt`；Vite 仍提示组件库主包较大，gzip 约 365 kB。

| 检查 | 实际证据 |
| --- | --- |
| 本地受监测 VLESS + TCP + REALITY + xtls-rprx-vision 入站，经用户授权的真实远程出口访问网站 | `reality-traffic.json` 最终 passed=true；5 次 HTTPS 请求全部 HTTP 200，百度、GitHub、YouTube 的域名日志均在本轮时间范围内确认 |
| 域名/IP/嗅探边界 | 传域名时记录百度/GitHub/YouTube；传百度 IP、仅服务器开启嗅探仍记录 IP；客户端开启嗅探后，同样的本地解析方式成功记录 `www.baidu.com`。报告分别保留 server=false、client=true，不宣称服务器嗅探必然还原域名 |
| 测试配置清理 | REALITY 脚本 finally 恢复原 Xray 模板、停用回环测试入站并关闭临时 SOCKS；私有连接配置只存 `runtime/monitor/private` |
| 新增清零后端权限与数据保护 | `TestPanelResetSessionScopeCSRFOneUseAndHistory`：无会话、错误 Origin/CSRF/会话、重复确认拒绝；官方 CSRF Cookie 更新保留，写请求从未使用后台 Token；旧用量及访问保留，后续差值正确 |
| 失败/重启衔接 | `TestPanelResetRejectsRenameExpiryAndKeepsDurableBaselineOnUncertainWrite`：改名/过期预览拒绝；官方拒绝与写后读取失败时持久化作废基线，SQLite 关闭重开后仍不把新计数当旧差值 |
| 官方创建时间精度兼容 | `TestPanelIdentityPrecisionMigrationKeepsHistoryBaselineAndDeletion`：毫秒变整秒不产生新身份，合并保留总量及最新基线；注入失败完整回滚；删除范围迁移后继续抑制历史重放，重复迁移不重复计数 |
| 实际数据库迁移 | `identity-migration-live.json`：原 28 条访问（Alice 7、Bob 4、REALITY 17）、28 条次数贡献与 10,750,759 字节历史总量完整保留；两组重复身份分别合并为一个，integrity_check=ok |
| 实际累计清零及手机确认 | 最终 `panel-reset-browser.json`：5 项通过，pageErrors/consoleErrors 均为空；在 390×844 手机尺寸下滚动到确认控件并完成清零，官方上下行 64/128 KiB 归零，589,824 字节历史采样前后相同，其他客户端计数相同 |
| 既有目标归类、合并、分钟查询与历史删除 | `enhancement-browser-checks.json`：8 项通过，pageErrors/consoleErrors 均为空；使用隔离数据库 |
| 原侧栏/顶部、深浅主题、历史导航、手机、独立页和退出 | `browser-checks.json`：10 项通过，pageErrors 为空；两条原版面板 fetch 失败发生于快速导航，失败类型均为 ERR_ABORTED |
| 运行会话、安全头、凭证隔离、7/35 配置及挂载盘 SQLite | 当前 `runtime-checks.json`：10 项通过、0 失败 |
| 最终主实例及清理状态 | `reality-reset-final.json`：health=ok、issues=[]；原 11 条加 REALITY 17 条记录完整保留，history_deletions=0、SQLite 完整；匿名预览/清零均 401，测试 11082/15444/15445 已关闭，清零测试客户端停用且累计为零，运行二进制与构建 SHA256 一致 |

下述为移除累计卡片前的历史验收记录；相应 UI 脚本现已删除，兼容 API 仍由后端测试覆盖。原专用 `scripts/test-panel-reset.py` 只调整 `monitor-reset-check` 的官方计数，待采样后调用 `scripts/browser-tests/panel-reset.mjs` 操作真实页面。验证域名服务标签、空日期跳转、精确客户端预览、停用客户端启用提示、勾选确认、真实累计归零、监测历史保留、其他客户端原始计数完全相同、重复确认拒绝。主实例中的 Alice/Bob/REALITY 累计不作清零演示；测试客户端及其入站结束后停用。

实际页面截图为 `monitor-real-reality-targets.png`、`monitor-latest-visit.png`、`monitor-panel-reset-preview.png`、`monitor-panel-reset-mobile.png`、`monitor-panel-reset-mobile-confirm.png` 和 `monitor-panel-reset-done.png`。

REALITY 复测曾发现官方 `inbounds/update` 保留停用状态，测试脚本已改为使用独立 `setEnable` 接口。另一次原始 IP 访问遇到连接超时，最终脚本采用有上限的一次重试；诊断报告保留在 `reality-traffic-*-diagnostic.json`，最终通过报告单独存放。HTTP 状态与 access log 的“已接受”分别校验，避免把代理接受连接当作网站访问成功。

源码/文档/脚本及 JSON 报告共 127 个文件检查未含本机密码、后台 Token 或用户提供的私有代理参数。测试访问只取网站首页，每次下载上限 1 MiB，不下载视频。临时客户端和测试配置不会进入版本控制。


## 2026-10-10 实时在线、域名字号与合计统一

最新页面移除“面板累计”卡片及清零弹窗，保留所选时间范围内的上传、下载和合计。流量趋势占满整行，服务名下方域名为 14px、高对比度且允许换行。实时列表复用现有采样并读取官方在线缓存；后台保留的官方清零兼容 API 不出现在当前页面。

| 项目 | 实际结果 |
| --- | --- |
| 后端测试、竞态和静态检查 | `backend-realtime.txt`：36 个命名测试通过；`backend-realtime-race.txt`、`backend-realtime-vet.txt` 通过；实时测试覆盖基线、空闲在线、上下行差值、入站/客户端筛选、回退、重置、断连、过时、鉴权和固定只读路由 |
| 前端测试与生产构建 | `frontend-realtime-tests.txt`：10 项通过；`frontend-realtime-build.txt`：TypeScript/生产构建通过，主 JS gzip 约 364 kB，仍有 Vite 大包提示 |
| 本地真实上下行 | `realtime-traffic.json`：VLESS/TCP 实际上传 4 MiB、下载 8 MiB，浏览器样本约 32/64 KiB/s，本轮约 320/640 KiB；停止后离线且上下行归零 |
| 实时 UI 与会话 | `realtime-browser.json`：5 项通过，pageErrors/consoleErrors 为空；自动轮询、精确客户端条件、日期/目标不混入实时参数、合计卡片、图表宽度、移动端、深浅主题、退出后 401 |
| 字号、服务规则、分钟删除 | `enhancement-browser-checks.json`：9 项通过；14px 域名、服务与端口分组、单分钟查询、单客户端删除隔离、所有客户端删除后该范围合计和记录归零、移动端和深色主题，浏览器无错误 |
| 原面板嵌入与导航 | `browser-checks.json`：10 项通过，未捕获未处理页面异常；快速导航取消请求时官方前端记录 3 条 fetch console 消息，详细路径保留在报告中 |
| 运行鉴权与存储 | `runtime-realtime.txt` / `runtime-checks.json`：10 项通过，包括会话/Token 隔离、安全响应头、持久日志配置、SQLite 完整性和挂载盘并发/异常恢复 |
| 最终服务与历史 | `realtime-final.json`：健康 ok、无 issues，数据库完整性 ok；Alice 7、Bob 4、REALITY 17 条全部保留，新增实时专用记录 4 条；临时端口全部关闭 |
| 新功能后空闲资源 | `resource-idle-realtime.json`：约 30.3 秒测量，监测 RSS 28.6 MiB、单核 CPU 0.17%；详见资源评估的条件与限制 |

实时测试使用独立 `monitor-live-check`；重复运行复用其身份、不清空其历史。报告中的官方计数包含此前中断测试的部分传输，实际本轮有界 payload 单列为 4/8 MiB。其他客户端官方累计完全相同，原有访问历史保留。临时本地路由恢复、15446 入站禁用，SOCKS 11083 和 HTTP 19093 关闭。删除验收仅操作 `18082` 的全新隔离数据库，主监测实例没有执行历史删除。

实时 API 返回内存快照；浏览器每次请求仍执行官方会话验证。首次采样、计数回退、数据过时均不虚构速度。在线状态跟随官方缓存，连接关闭不承诺瞬时变化。截图：`monitor-realtime-light.png`、`monitor-realtime-dark.png`、`monitor-realtime-mobile.png`、`monitor-service-summary.png`。


## 2026-10-10 删除客户端残留选项修复

问题来自监测客户端列表返回所有历史身份，即使该身份已无访问记录、流量和访问次数，也会留在查询和删除弹窗中。相同 email 在测试期间多次删除重建，进一步造成名称重复。现在默认只显示当前客户端，勾选“显示历史客户端”才列出仍有保留期内数据的已删除身份，并显示最后同步时间及身份编号。

客户端 API 通过时间/客户端索引检查剩余明细及汇总。清空最后一段历史后，选项立即消失；仅有官方旧累计数值、质量标记或身份映射不再维持一个历史选项。身份映射和删除防重导入记录继续保留，35 天内只有汇总的客户端仍可查询和清理。已经选中的历史客户端清空后，筛选恢复为全部客户端，避免显示内部 ID。

后端 `backend-client-options.txt`：37 项通过，`backend-client-options-vet.txt` 无诊断。新增回归覆盖空历史、同名现存客户端、8～35 天汇总、清理后隐藏、身份映射保留与到期前置过滤。前端 `frontend-client-options-tests.txt`：10 项通过，TypeScript/生产构建报告为 `frontend-client-options-build.txt`。

实际实例只读核对见 `client-options-final.json`：5 个当前客户端、4 个有历史的旧身份、4 个无记录旧身份已从 API 隐藏；原访问记录数保持 Alice 7、Bob 4、REALITY 17、实时测试 4，健康与 SQLite 完整性正常。官方当前的 Alice/Bob 客户端仍存在，本次修改没有删除或重建官方客户端。

浏览器最终验证：`client-options-browser.json` 和 `client-options-normal-motion-browser.json` 各 3 项通过，分别覆盖减少动画/正常动画下实际实例的默认列表、历史开关、同名区分、关闭开关清除历史选择、删除弹窗和手机布局；未处理页面异常为空。`enhancement-browser-checks.json` 10 项通过，包括选中历史客户端后清空记录、恢复“全部客户端”、旧身份从 API 消失、另一个客户端保留和全选清理。

检查发现全局 `transition-duration:.001s` 会使减少动画模式下的弹窗定位/尺寸测量也发生过渡，导致下拉框停在屏幕外。现在该模式使用 0s 过渡，保留短时 keyframe 动画结束事件，最终两个动画模式均可正常点击。截图 `monitor-current-clients.png`、`monitor-client-picker-mobile.png`、`monitor-history-clients.png`。

## 2026-10-10 原面板动态模块、完整容量与峰值

用户报告 `ClientQrModal-Bt-NPyL_.js` 加载失败。直接上游和代理当前均可取得该模块，静态依赖遍历的 27 个文件均 HTTP 200；无法追溯最初一次请求失败的具体网络原因。故障注入已复现原错误：官方单次自动刷新额度耗尽后中断二维码模块请求，页面出现相同的 `Unexpected Application Error`。现在保留官方资产压缩/缓存机制，对临时资产错误有限重试，并在模块入口之前加载独立恢复控制。

| 验证 | 最终结果与报告 |
| --- | --- |
| 后端常规测试 / 静态检查 | 39 个命名测试，`backend-panel-proxy-tests.txt` 通过；`backend-panel-proxy-vet.txt` 无诊断；3 个资源测试仅显式启用时执行 |
| 资产代理回归 | `TestPanelModuleRetryIsBoundedAndReadOnly` 验证临时失败重试、上限、取消、不重试 API/POST/404/401；`TestProxyPreservesModulesAndInstallsRecoveryBeforeEntry` 验证 gzip 字节、缓存校验、安全头、子路径、未知版本恢复脚本顺序，以及 404 不继承上游一年缓存 |
| 前端 | `frontend-panel-recovery-tests.txt`：10 项通过；`frontend-panel-recovery-build.txt`：类型检查和生产构建通过，保留已有 Vite 主包大于 500 kB 的提示，主包 gzip 约 365 kB |
| 浏览器故障复现 | `panel-recovery-baseline.json`：用尽官方恢复次数后模拟一次请求失败，重现同一二维码模块错误 |
| 浏览器故障恢复 | `panel-recovery-browser.json`：提示与独立监测链接可见，手动重新加载后二维码弹窗正常，无循环刷新 |
| 原面板功能 | `official-panel-browser.json`：17 项通过，103 个不同 JS/CSS 资源成功取得；页面异常、HTTP 错误均为空，原 WebSocket 有真实帧 |
| 功能具体范围 | 客户端真实二维码渲染、信息/编辑/添加弹窗；入站编辑/添加弹窗；系统状态、分组、节点、主机、出站、路由、面板设置、Xray 配置、API 文档、赞助商；设置 6 个子页和 Xray 4 个子页；监测→客户端→二维码→后退/前进/刷新 |
| 官方数据保护 | `official-functions.json`：只创建一个随机命名的临时回环入站/客户端生成二维码，最终移除；所有原客户端和入站对象前后完全相同 |
| 原嵌入行为 | `browser-checks.json`：10 项通过，深浅主题、手机、固定侧栏、筛选、历史导航、未知版本回退、独立页、退出均正常，无页面异常 |
| 查询与删除 | `enhancement-browser-checks.json`：10 项通过，包括 App/网站归并、分钟范围、历史身份、单客户端与所有客户端删除；仅操作隔离数据库 |
| 运行会话与文件系统 | `runtime-panel-proxy.txt` / `runtime-checks.json`：10 项通过，包括凭证隔离、安全头、退出/过期、保留规则、SQLite 完整性及挂载盘异常恢复 |
| 完整 35 天容量与并发 | `resource-peak-20261010T080241-12722.json`：70 万明细、252,000 分钟流量、251,995 访问次数桶、350 万次数；数据库 290.1 MiB，峰值 RSS 34.2 MiB，整周目标查询 17～21 秒，采样最长等待约 21 秒 |
| 5 客户端 REALITY | `resource-reality-5clients.json`：本地回环、20/100 Mbps、各 30 秒；累计 450 MB，Xray 服务端峰值 RSS 33.7 MiB、CPU 一秒窗口峰值约 5%；临时进程全部关闭 |
| 原面板操作期间真实进程 | `resource-panel-navigation.json`：约 45 秒，三项合计峰值 207.1 MiB、CPU 窗口峰值 8.91%；无业务转发，不代表整机满载 |
| 最终状态 | `panel-proxy-final.json`：health=ok、issues=[]、完整性 ok，32 条原访问保留，history_deletions=0，5 个现存客户端；临时官方对象已移除，运行二进制和恢复脚本与构建/源码一致，保留期 7/35 |

所有浏览器验收使用 Chromium 与官方 v3.9.0，入口为实际 `http://localhost:18080`（旧监测专用套件使用 127.0.0.1）。编辑/设置弹窗均打开后关闭；未配置的远程节点、机器人、邮件等外部集成未做真实外部连接验证。页面快速跳转主动取消的请求保留在报告，未出现非取消类型的失败。

资源结论、旧 150 MiB 估算的修正原因、35 天日志假设和 VPS 边界详见 `resource-assessment.md`。测试不承诺特定 VPS 满速上限，也不将长范围数据库等待描述为零开销。

## 2026-10-12 Docker 与 Linux 管理命令

隔离环境：WSL Ubuntu-24.04，Docker 29.1.3 / Compose 2.40.3，官方 3.9.0；独立端口 28080/28081、内部 22053/63789/21111。本轮没有启动原生开发实例、没有使用其数据库或凭证，没有部署实际服务器。

| 检查 | 结果 |
| --- | --- |
| 容器整体验收 | `docker-acceptance.json`，15 项全部通过 |
| 新部署 | 随机凭证、非根访问路径、共享登录、未登录 API 拒绝、7/35 天保留配置 |
| 真实代理 | 两个 VLESS/TCP/REALITY/xtls-rprx-vision 客户端；每客户端 1 MiB 下载与 128 KiB 上传，4 条本地目标记录，2 组目标汇总，方向和身份正确 |
| 官方面板浏览器 | `docker-official-browser.json`，17 项通过；二维码、客户端/入站弹窗、官方侧栏与各设置页、前进后退刷新、WebSocket；未出现动态模块加载错误 |
| 监测页面浏览器 | `docker-monitor-browser.json`，4 项通过；嵌入/汇总、分钟选择/明细/实时/删除弹窗、深色/移动端/刷新、独立页/登录校验 |
| 管理命令 | 启停、账号重置、路径/公开端口、HTTPS 导入/关闭及 Secure Cookie、Xray 重启、采集 Token 重建、二步验证重置、自启动策略、命令安装和菜单 |
| 删除、恢复、更新 | 实际删除全部测试记录及流量后恢复；同镜像重建；故意失败镜像更新后自动恢复旧镜像与数据库；三张历史表逐项 SHA-256 与备份一致 |
| 跨目录日志恢复 | 修复恢复后文件标识变化造成的重复读取；3 项专项测试验证标识重绑定、未读尾部保留、篡改/缺失清单拒绝；显式关闭 SQLite 句柄以支持 Windows 挂载盘目录移动 |
| 卸载 | 删除测试容器，保留数据及备份，无残留测试端口 |
| Go / Python / Shell | Go 全套常规测试通过（新增 TLS 路径和会话 Cookie 测试）；`go vet ./...` 无诊断；Python/Shell 语法检查通过 |

REALITY 含后量子参数时，官方 3.9.0 的 `ClientQrModal` 通过 `isPostQuantumLink` 主动隐藏二维码图像，链接仍可取得。实际 QR 渲染验收使用单独的普通 VLESS 测试客户端，不把官方这一行为当成模块故障。

资源数字和统计口径见 `resource-assessment.md` 的 Docker 增补。当前交付固定为 Linux amd64、官方 3.9.0；验证了同镜像重建与失败更新恢复，未来官方新版本的 CLI/布局仍需针对版本验收。外部节点、机器人、ACME、主机防火墙/BBR 等未列入此次容器验收。

## GitHub 安装入口与 1.0.1 修订

新增 `scripts/install-online.sh`，从 `eatfatno/x-ui-tab` 的指定稳定 Release 下载 amd64 部署包及校验文件。归档内外分别验证 SHA-256，拒绝路径越界、链接、运行数据、版本/架构不符及已有部署覆盖。发布准备脚本生成独立的源码目录和清单，实际上传使用 Windows Git 身份验证。

一键安装的首次 Docker 验证在较慢的 WSL 挂载盘复现了启动竞态：复制 Core 期间健康检查读取设置，用普通 SQLite 连接提前创建空数据库，使首次账号初始化被跳过。1.0.1 将设置读取改为 SQLite 只读模式。专项测试连续模拟提前读取，确认未创建数据库，随后能读取真正初始化的设置。

| 检查 | 证据与范围 |
| --- | --- |
| 在线包校验与解压 | `online-installer-tests.txt`，14 项检查 |
| 首次启动竞态 | `docker-initialization-tests.txt`，只读 SQLite 回归检查 |
| 实际安装、登录和重复安装保护 | `online-install-docker.json`；下载由本地夹具模拟，后续导入/启动/登录使用真实 1.0.1 Docker 包 |
| 1.0.1 容器回归 | `docker-acceptance.json`，本轮脚本的 13 项功能检查全部完成；官方面板 17 项、监测页面 4 项也通过 |
| 源码发布清单 | `artifacts/github/source-manifest.json`；检查常见凭证格式及本地私有配置敏感值 |

此轮发布前测试尚未覆盖真实 Release 下载链路，后续公开下载验收见下一节。干净 Ubuntu 的 apt 依赖安装没有在本机执行。WSL 测试使用已有 Docker/Python，未修改宿主机软件源。新增安装入口支持首次安装，在线更新菜单与 Actions 自动构建不在此版本中。

本轮完整功能回归全部完成后，资源报告生成器遇到 Docker 容器退出期间的 `--` CPU 读数。已修复为跳过不可用样本；根据原始 PASS 输出、最终数据库和原始资源采样重建报告，原因保留在 `docker-acceptance.json` 的 `report_recovery` 字段，原始执行日志未改写。此问题不在面板或监测程序运行路径中。

## 2026-10-15 公开发行包最终验收

正式 Release v1.0.1 和标签源码均可公开读取。未使用 GitHub 登录凭证，没有将本地下载模拟算作公开下载。包内容没有改动，因此既有功能、资源与恢复验收继续对应相同镜像。

| 检查 | 结果与范围 |
| --- | --- |
| GitHub 脚本 | 官方 API 原始响应与已审核安装脚本逐字节一致，SHA-256 `fd64d583fd693c5071db794d9e16edaac2e9ebd618caddec8629014478dbe19a` |
| 真实完整附件 | `public-release-download.json`；HTTPS 分段取回 177,133,451 字节，SHA-256 `fe43e21f97e0f9b96033f1e372fc0ed0b66eae18d7552867499d9c5fa5323a5a`，与 GitHub digest 及校验文件一致 |
| 公开包安装 | `public-bundle-install.json`，7 项通过；核对包内哈希、实际导入镜像、安装管理入口、新凭证登录、空库及 7/35 天配置、匿名拒绝、重新安装保护 |
| 实际浏览器 | 同报告的 `browser` 字段，4 项通过；官方侧栏与嵌入、分钟与明细/删除弹窗、刷新与手机布局、独立页与会话删除；页面异常为空 |
| 单连接在线流程 | `public-online-install.json`；脚本取得正常，大文件下载超过测试的 600 秒限制，未判为通过。原始 raw 地址也被当前网络重置，已提供官方 API 备用入口 |
| 清理与隔离 | 测试容器移除，数据和日志只留在项目夹具内；未操作用户现有 WSL 服务、OpenCode 或默认 Docker daemon |

这里验证的是“真实公开下载后安装”；不把分段下载后的离线安装称为单连接一键流程通过。干净系统的 apt 依赖安装和未指定的实际 VPS 部署仍未执行。
