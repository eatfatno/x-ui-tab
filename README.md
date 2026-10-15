# x-ui-tab：基于 3x-ui 的非官方流量监测扩展

x-ui-tab 在原版 3x-ui 面板基础上增加流量监测、访问目标识别和部署管理功能，由独立的监测服务与页面实现。本仓库是社区扩展，不是 3x-ui 官方项目或官方发行版。

- **上游原项目**：[3x-ui](https://github.com/MHSanaei/3x-ui)，由 [MHSanaei](https://github.com/MHSanaei) 及其贡献者维护。原面板和代理管理功能来自该项目，相关版权归原作者及贡献者所有。
- **本扩展项目**：[eatfatno/x-ui-tab](https://github.com/eatfatno/x-ui-tab)，由 [eatfatno](https://github.com/eatfatno) 维护，负责本仓库的流量监测扩展、集成与部署工具。
- **当前适配版本**：官方 3x-ui **v3.9.0**。官方面板与 Xray 使用上游发行包，不直接修改官方二进制、Xray 内核或面板内嵌前端。

## 在原项目基础上增加了什么

- **流量监测 Tab**：通过同源代理和独立页面，在保留官方侧栏与顶部的同时增加监测入口；也支持独立页面访问，复用官方登录会话。
- **客户端流量与实时状态**：查看所选时间范围内的上传、下载、合计和趋势，以及客户端在线状态、当前上传/下载速度；时间筛选精确到分钟。
- **访问目标明细与服务汇总**：记录实际访问的域名/IP、端口和协议，通过域名规则识别百度、YouTube、Telegram、OpenAI、淘宝等服务，同一服务的网页与 App 访问合并展示。
- **历史保留与删除**：访问明细最多保留 7 天，客户端流量和每日访问数量汇总最多保留 35 天；支持按客户端和时间范围删除监测记录及对应的采样流量。
- **独立存储与升级适配**：监测配置和历史数据独立保存，官方更新不会覆盖这些文件；界面适配异常时仍可使用独立页面。
- **Docker 部署与管理工具**：提供本项目的安装脚本和 Linux `x-ui` / `x-ui-tab` 管理菜单，支持启停、备份恢复及镜像更新等操作。

流量用量来自客户端累计值采样，访问目标来自 Xray 日志；不提供每个目标或每条连接的精确字节数，也不提供 HTTPS 完整路径或页面内容。域名规则来自社区规则集，来源和许可证见 [第三方组件说明](THIRD_PARTY_NOTICES.md)。本项目代码采用 [GPL-3.0](LICENSE)，第三方组件保留各自的版权和许可。

## 在 Linux 服务器安装

面向 **Linux x86_64 / amd64**。一键脚本在 Ubuntu 22.04/24.04 上可安装缺少的 Docker、Compose v2 和 Python；其他 Linux 发行版请先准备 Docker、Compose v2、Python 3.10+、curl 和 util-linux，并在命令末尾增加 `--no-install-deps`。

**[v1.0.1 Release](https://github.com/eatfatno/x-ui-tab/releases/tag/v1.0.1) 已发布**，包含部署包及校验文件。执行：

```bash
curl -fsSL https://raw.githubusercontent.com/eatfatno/x-ui-tab/v1.0.1/scripts/install-online.sh | sudo bash -s -- --version v1.0.1
```

如果当前网络访问 `raw.githubusercontent.com` 被重置或超时，可改用 GitHub 官方 API 取得同一版本的脚本，无需 GitHub 登录：

```bash
curl -fsSL -H 'Accept: application/vnd.github.raw+json' 'https://api.github.com/repos/eatfatno/x-ui-tab/contents/scripts/install-online.sh?ref=v1.0.1' | sudo bash -s -- --version v1.0.1
```

两个入口执行同一脚本，均从 GitHub Release 下载部署包；需要能访问 GitHub API 及 Release 附件 CDN。如果服务器没有 `curl`，Ubuntu 可先执行 `sudo apt-get update && sudo apt-get install -y curl ca-certificates`。

默认安装目录 `/opt/x-ui-tab`，面板入口 `http://服务器IP:18080/panel/?monitor=traffic`。首次账号密码文件 `/opt/x-ui-tab/data/state/credentials.json`，密码随机生成。执行 `sudo x-ui` 打开管理菜单；如果已有其他 `x-ui` 安装，使用 `sudo x-ui-tab`。

脚本下载指定的稳定发布包，检查整体及包内 SHA-256，再导入镜像和启动。已有部署会拒绝重新初始化，请使用管理菜单更新。更换目录/端口可增加 `--dir /opt/my-x-ui --port 18081`；共存多个实例还需分别指定 `--panel-port`、`--api-port`、`--metrics-port` 和 `--project-name`。安装不自动放行云防火墙端口。

发布与公开安装验收见 [GitHub 发布说明](docs/github-publishing.md)。GitHub Actions 自动构建与在线升级菜单尚未接入，当前镜像升级方式见下方 Docker 文档。

Docker 部署及 Linux `x-ui` 管理菜单见 [Docker 部署说明](docs/docker-deployment.md)。部署包面向 Linux amd64，包含官方 3.9.0 和流量监测，镜像与持久数据分开。`x-ui` 支持启停、状态、日志、账号/路径/端口、HTTPS、备份恢复和镜像更新；首次凭证在所部署目录的 `data/state/credentials.json`。WSL 原生开发环境仍按下文管理，凭证位置与 Docker 环境不同。

支持本地域名规则识别（1,411 个服务组、24,541 条域名规则、14 条 Telegram 官方专属网段），可按百度、GitHub、YouTube、Telegram、X、OpenAI、淘宝、抖音等名称搜索。同一客户端访问同一服务的网页、App、接口、媒体域名及端口合并展示。查询和“删除日志与流量”均精确到分钟，包含选择的结束分钟；删除会同步清除该客户端时段内的明细、访问次数及采样用量。使用和规则更新说明见 `docs/targets-and-deletion.md`；完整服务目录为 `apps/traffic-monitor/internal/monitor/rules/service-catalog.json`。

页面的实时在线列表约每 10 秒更新，历史统计和当前打开的记录表每 30 秒刷新，后台每 10 秒采集。针对 1 核 / 1 GB 内存 / 20 GB 磁盘、最多 5 台设备的 WSL 测量见 `docs/resource-assessment.md`。

页面保留 **合计流量**，按所选时间范围显示上传加下载；已按最新需求移除“面板累计”卡片和清零入口。清理使用“删除日志与流量”，选择日期/分钟及一个或所有客户端，预览确认后合计同步减少。**实时在线客户端**另行显示在线状态、上传/下载速度及最近一轮上传/下载量，复用已有采样，不为每条访问估算字节数。

客户端选择框默认只列当前客户端；勾选“显示历史客户端”可查看仍有保留数据的已删除身份。已清空或到期的旧身份自动从选项中消失；“所有客户端”的查询/清理范围仍包含历史客户端。

## 本机打开

在 WSL Ubuntu-24.04 内运行，Windows 浏览器访问：

**http://localhost:18080/panel/?monitor=traffic**

独立页面：**http://localhost:18080/traffic-monitor/**。使用官方面板同一登录会话。

首次创建的随机登录密码保存在 `runtime/lab-state.json`，用户名为 `monitor-admin`。该文件和 `runtime/monitor/api-token` 是本机私有配置，不提交到 Git。首次登录后可以在原面板修改密码；运行验收脚本前需同步本地测试凭证。

PowerShell 启动、停止与状态查看：

```powershell
wsl -d Ubuntu-24.04 -- bash /mnt/d/work/codexworkspace/x-ui-tab/scripts/start.sh
wsl -d Ubuntu-24.04 -- bash /mnt/d/work/codexworkspace/x-ui-tab/scripts/stop.sh
wsl -d Ubuntu-24.04 -- python3 -B /mnt/d/work/codexworkspace/x-ui-tab/scripts/lab.py status
```

这些脚本仅管理本项目 PID，默认不安装系统服务、不修改 WSL 启动配置。测试客户端以 `monitor-test-` 命名，验收结束后所属入站禁用；页面中留下的记录来自实际本地代理流量。

## 目录与构建

代码在 `apps/traffic-monitor`，前端在其 `web` 子目录。官方包放入 `upstream/3x-ui`，工具链放入 `tools`，运行配置、数据、日志和缓存分别位于 `runtime`、`data`、`runtime/logs`、`cache`，测试结果在 `artifacts`。

所有 WSL 构建和测试先加载项目环境，确保临时文件及缓存不散落到其他目录：

```bash
cd /mnt/d/work/codexworkspace/x-ui-tab
source scripts/env.sh
bash scripts/build.sh
python3 scripts/lab.py restart --only monitor
```

从干净目录准备官方包和 Go：运行 `python3 scripts/fetch-upstream.py`，随后用 WSL `tar` 把 `tools/downloads/go1.27.1.linux-amd64.tar.gz` 解压至 `tools`，把 `upstream/3x-ui/v3.9.0/x-ui-linux-amd64.tar.gz` 去除顶层目录解压至 `runtime/panel`。最后执行 `python3 scripts/lab.py init`、构建并启动。发行包和 Go 下载均做 SHA-256 校验。

## 数据说明

- **所选日期用量**：每 10 秒读取面板持久累计值后得到的差分；首次采样仅建立基线。不能反推启用前的日期历史。
- **合计与实时**：合计为所选分钟范围内的历史采样用量；实时为最近两次采样差值除以实际间隔得到的平均速度。实时仅应用客户端/入站条件，首次采样、断采、累计回退显示未知值，在线状态来自官方在线名单；短连接关闭后可能延迟一个采样周期变为离线。
- **请求目标**：Xray access log 记录的域名/IP、端口和协议。HTTPS 不提供完整 URL、页面内容或搜索词。每条代理记录也不等同于一次网页浏览。
- **空日期与 REALITY**：默认查询今天；当天无记录、其他日期有记录时，提示最近访问时间并可跳转。REALITY 的伪装 SNI 不作为访问目标。客户端传域名时按规则识别；只有 IP 的日志仍展示 IP。如何保留真实域名见 `docs/targets-and-deletion.md`。
- **目标汇总**：按客户端和域名规则识别结果分组，展示访问次数、原始域名/IP、端口、协议、入站和首末时间；与访问明细同样限于最近 7 天。目标没有独立字节数，客户端用量在上方卡片及趋势查看。
- **删除历史**：按客户端及日期同时删除全部目标和入站的监测记录及流量，不修改原面板累计和限额。原始 Xray 文件仍按 7 天清理；删除范围禁止重新导入。包含当前时段时，预览显示截至当前分钟结束的实际范围。
- **过期与缺口**：第 8～35 天只有客户端流量及访问数量汇总。重置、断采、未匹配身份、日志异常分别提示；不会把未知用量自动填成零。
- **入站筛选**：访问记录按实际入站筛选；客户端累计值本身是跨所属入站的总量，不能推导同一客户端在各入站分别用了多少字节。

## 更新与排障

参见 `docs/operations.md`。原版二进制可替换升级，监测程序和数据在独立目录。入口依赖官方 3.9 布局适配器，未知版本使用独立页面，并按检查结果更新适配器。

原面板 JS/CSS 保留官方压缩与缓存机制，临时模块请求故障有有限重试和页面恢复入口。如果旧标签页已经显示 `Failed to fetch dynamically imported module`，先 Ctrl+F5 加载新版恢复脚本。原面板页面、二维码/编辑弹窗及故障恢复验收见 `docs/test-matrix.md`，1 核峰值和完整 35 天容量见 `docs/resource-assessment.md`。

开发大纲：`docs/development-outline.md`；旧环境清理记录：`docs/environment-check.md`；接口：`docs/api-contract.md`；验收矩阵：`docs/test-matrix.md`。

本机页面采集的是本机 WSL 面板及日志。远程 VLESS 分享链接仅提供代理连接能力，不提供远程面板数据读取权限；查看远程设备的日常历史，需要在对应服务器接入本监测服务。
