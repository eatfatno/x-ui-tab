# v1.0.2：升级到官方 3x-ui v3.10.2

本项目基于 [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)，由 eatfatno 维护独立流量监测与 Docker 部署扩展。此次使用官方 [v3.10.2](https://github.com/MHSanaei/3x-ui/releases/tag/v3.10.2) Linux amd64 发行包，附带 Xray 26.10.10，不修改官方二进制或内嵌页面。发布日期为 2026-10-15。

## 本次变化

- 前端布局适配器和服务端版本判断加入精确的 3.10.2 支持，保留原 3.9.x 支持；未经验证的其他新版本继续使用独立页面保护。
- Docker 默认镜像更新为 `x-ui-tab:3.10.2-1.0.2-amd64`。监测数据库、客户端身份、规则、分钟查询和 7/35 天保留口径不变。
- 对官方新的 REALITY 二维码行为和所有已有侧栏页面、弹窗进行浏览器验收；重新测试模块请求中断后的恢复入口。
- 增加从已发布 v1.0.1 生成真实访问与流量，再迁移到新版的测试；注入官方数据库迁移之后的故障，检查恢复旧数据库与旧镜像。
- 一键安装从 GitHub 官方 API 取得附件并分段下载。每段最大 1 MiB，最多 8 个并发，校验字节范围与长度，网络失败有限重试，再核对整体和包内 SHA-256。
- 开发准备脚本改为只下载已校验的官方 Linux 包，不再获取完整官方源码归档。

## 已有 v1.0.1 Docker 部署如何升级

在服务器下载 [v1.0.2 Release](https://github.com/eatfatno/x-ui-tab/releases/tag/v1.0.2) 中的部署包和 `.sha256`，放到一个新目录。假设现有部署位于 `/opt/x-ui-tab`：

```bash
sha256sum -c x-ui-tab-3.10.2-1.0.2-linux-amd64.tar.gz.sha256
tar -xzf x-ui-tab-3.10.2-1.0.2-linux-amd64.tar.gz
cd x-ui-tab-3.10.2-1.0.2-linux-amd64
sha256sum -c SHA256SUMS

sudo /opt/x-ui-tab/x-ui load "$PWD/image.tar.gz"
sudo /opt/x-ui-tab/x-ui update x-ui-tab:3.10.2-1.0.2-amd64
sudo /opt/x-ui-tab/x-ui status
```

升级期间会短暂停止代理连接，自动制作包含数据库、配置、凭证、Core 和日志的一致性备份。启动检查失败会恢复旧镜像和升级前的数据库；备份位于原部署的 `backups/`。成功后进入原入口、刷新页面，确认采集正常、旧访问与流量可查。执行升级命令的始终是**原部署目录中的 x-ui**，不要在新下载目录重新初始化。

本次宿主管理脚本的菜单和配置格式保持兼容，无需覆盖现有 `compose.yaml`、`.env` 或 `data/`。需要同步脚本时，可在包内哈希验证通过后用 `sudo install -m 755 x-ui /opt/x-ui-tab/x-ui` 更新管理程序。载入后可删除解压包中的 `image.tar.gz` 及下载归档来节省空间；保留升级备份和旧镜像直到确认业务正常。备份不受 7/35 天自动清理规则管理。

官方数据库含新字段，不能直接指定旧镜像强行降级。如需回退，使用原管理工具恢复升级前备份：

```bash
sudo /opt/x-ui-tab/x-ui restore /opt/x-ui-tab/backups/实际升级前备份.tar.gz --yes
```

该命令需要旧版镜像仍在本机。真实服务器原生安装的官方 3x-ui 不属于这份 Docker 数据升级流程；不能把原生数据目录直接当成本项目部署目录。

## 验证记录

构建复用 v1.0.1 已验证的 Alpine 运行依赖层（通过 Docker `--cache-from`）；官方程序、Xray、监测程序及页面全部重新装入新版，未宣称容器系统软件包均已更新到仓库最新。完整依赖重新下载因本地网络缓慢中止，相关构建日志保留。

以下在 WSL Ubuntu 24.04 的项目专用 Docker daemon 验证完成，使用临时客户端和本地 TLS/HTTP 目标，不调用用户的真实代理节点：

| 验证 | 结果 |
| --- | --- |
| 后端 / 前端 | Go tests、vet 通过；前端 15 项测试、TypeScript 与生产构建通过 |
| 安装和恢复工具 | 17 项安装器、1 项初始化竞态、3 项日志检查点恢复测试通过 |
| 原生开发实例 | 已升级到 3.10.2，登录、权限、查询及 SQLite 恢复等 10 项通过 |
| 新版 Docker 功能 | 14 项通过，包括真实 REALITY 流量、实际日志轮转、分钟删除、设置管理、TLS、备份恢复和更新失败恢复 |
| 官方页面 | 18 项浏览器检查通过，107 个静态资源正常，包含普通 VLESS/REALITY 二维码及侧栏页面 |
| 监测页面 / 模块恢复 | 4 项监测浏览器检查及 2 项动态模块故障恢复检查通过 |
| 真实旧版升级 | 已发布 3.9.0/1.0.1 镜像产生的 4 条访问和非零流量，在升级、重启及故障回退后保留，客户端、账号和配置一致 |
| 官方迁移后的回退 | 新版实际将旧 xdns 的 name/typed resolver 转换为 names/addrs；模拟监测进程失败后，旧字段格式、旧镜像、数据和登录均恢复；随后成功升级 |
| 降级保护 | 直接更新到旧版镜像被拒绝，历史不变；回退需恢复配套备份 |

第一次迁移探针只比较新增列，没有触发配置格式变化，因此没有把它当作迁移成功证据。完善后的测试采用官方迁移测试中的旧 xdns 配置，分别核对迁移后和回退后的值，不仅检查容器是否启动。监测数据库自身未改变结构。

原始报告位于项目 `artifacts/reports/`，主要报告随离线包附带于 `reports/`：`docker-acceptance.json`、`docker-version-upgrade.json`、`docker-official-browser.json`、`docker-monitor-browser.json` 和 `panel-recovery-browser.json`。解压包安装用 `scripts/verify-docker-bundle.py` 验证；公开发布后的真实在线下载与安装用 `scripts/verify-public-install.py` 独立记录，后续结果追加到主分支文档。

## 适用范围

交付 Linux amd64 Docker 包；支持使用既有 Docker Engine、Compose v2 和 Python 3.10+ 的 Linux 服务器。此次实际验证环境为 WSL Ubuntu 24.04，未在真实 VPS、CentOS 或全新 Ubuntu 上执行依赖安装。新增 MASQUE、XDRIVE、TUN、外部节点和机器人不属于本次验收范围。

资源测量见 [资源说明](resource-assessment.md)。月流量 300 GB 不等于瞬时峰值；35 天容量仍主要由连接条数决定。Windows Defender 对旧版完整源码归档的隔离记录保留，未恢复文件、关闭防护或添加排除项；此次校验的官方 Linux 包哈希为 `bfda7921984e7a72555c38d4e861c7a8b67b3686b658608937de99682f1452d0`。


## v1.0.2 发布后的真实在线安装复验

2026-10-15 已从 GitHub 公开地址执行本版本安装脚本，完成 7 项流程检查和 4 项 Chromium 检查。此验证没有使用 GitHub 凭证、本地下载模拟或预先放入下载目录的部署包；安装器实际从 GitHub 下载完整的 **180,348,292 字节**附件，验证整包与包内哈希，导入指定镜像并启动新实例。

新凭证登录、匿名访问拒绝、7/35 天保留配置、嵌入/独立页、分钟筛选、删除弹窗、手机布局、刷新及重复安装保护通过；测试容器已经移除。报告为 `artifacts/reports/public-online-install.json`，另有 `docker-bundle-verification.json`（4 项实际解压包检查）和 `online-install-docker.json`（4 项下载夹具与实际 Docker 安装检查）。三种测试分别记录，不混用结果。

本地网络仍无法直连 raw.githubusercontent.com；成功的完整流程使用文档中的 GitHub 官方 API 脚本入口。附件下载同样走 GitHub 官方 API/CDN。干净系统的 apt/Docker 依赖安装、真实 VPS 和其他 Linux 发行版仍未实机验证。

正式附件 SHA-256：`f6bdcdbba7dfd8893bf991a2208a6c9e16180291ea10f31450e841e5f9332eab`。对应功能发布提交为 `2773f5574e3b0315b579ae8ca0c2f62431fbd73a`，v1.0.2 标签和附件保持不变；本节作为发布后验证记录补充到主分支。
