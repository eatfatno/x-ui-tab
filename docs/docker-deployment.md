# Docker 部署与 Linux x-ui 管理

此部署包包含官方 3x-ui v3.9.0、Xray、独立 Go 流量监测服务及 React 页面，通过 **Docker Engine + Compose v2 直接部署到 Linux 服务器**，不需要 Windows 或 WSL。目标平台为 **Linux x86_64 / amd64**，当前未交付 ARM 镜像。

## 系统环境与依赖

Ubuntu、CentOS Stream、Debian、Rocky Linux、AlmaLinux 等发行版使用相同 Docker 镜像和管理命令。宿主机需要正在运行的本地 Docker Engine、Compose v2、Python 3.10+，以及 Bash、curl、tar/gzip、coreutils 和 util-linux。`python3` 命令本身必须满足版本要求，因为安装和 `x-ui` 管理工具在宿主机运行；只在容器里提供 Python 不足以运行管理工具。

- **Ubuntu 22.04/24.04**：在线安装脚本可准备缺少的依赖。
- **CentOS Stream 等其他 Linux**：先按该发行版的方式安装上述依赖，再使用 `--no-install-deps`，或按下文手工部署。脚本不会自动配置这些发行版的软件源。

可以用 `sudo docker info`、`sudo docker compose version`、`sudo python3 --version` 检查环境。当前 Docker 包的实际安装与浏览器验收在 WSL Ubuntu 24.04 完成；干净 Ubuntu 的自动依赖安装、CentOS Stream 及其他发行版尚未实机验收。

CentOS Stream 等宿主机若启用了 SELinux 容器访问控制，需要为专用数据目录配置容器标签。当前 Compose 文件未自动添加标签：可采用下文的手工部署流程，在包校验完成后、首次 `x-ui init` 前，将 `compose.yaml` 中的数据卷 `./data:/data` 改为 `./data:/data:Z`，由 Docker 为该部署的专用目录设置标签。该目录应仅供本部署使用。

## 新服务器安装

已发布版本可直接使用 [GitHub 一键安装命令](github-publishing.md#3-服务器安装)。下列步骤适用于手工传输部署包的离线安装；两种方式部署相同镜像，使用相同管理命令。

把 `artifacts/docker/x-ui-tab-3.9.0-1.0.1-linux-amd64.tar.gz` 和旁边的 `.sha256` 文件复制到服务器。压缩包不含开发机的数据库、账号密码、代理链接或访问历史。

```bash
sha256sum -c x-ui-tab-3.9.0-1.0.1-linux-amd64.tar.gz.sha256
tar -xzf x-ui-tab-3.9.0-1.0.1-linux-amd64.tar.gz
cd x-ui-tab-3.9.0-1.0.1-linux-amd64
sha256sum -c SHA256SUMS
sudo bash install.sh
sudo x-ui load
sudo x-ui init
sudo x-ui settings
```

镜像导入与启动不需要连接镜像仓库。宿主机安装 Docker/Python 自身所需的软件源由服务器管理。镜像中的软件、Geo 文件和服务分类规则为构建时版本。

首次凭证位于部署目录的 **`data/state/credentials.json`**，通过服务器终端自行查看。初始用户名为 `monitor-admin`，密码随机生成。默认入口为 `http://服务器IP:18080/panel/?monitor=traffic`；生产使用可导入 HTTPS 证书。

```bash
sudo x-ui                         # 打开交互管理菜单
sudo x-ui credentials             # 交互修改用户名、密码；密码输入不回显
sudo x-ui base-path /my-panel/     # 同步官方面板和监测页面的访问路径
sudo x-ui port 18443              # 修改对外面板端口；不改变代理入站端口
sudo x-ui tls /path/fullchain.pem /path/privkey.pem
sudo x-ui settings
```

TLS 导入会校验并复制证书/私钥至 `data/certs/`。更新证书后再次执行 `x-ui tls`，会重启服务并应用新证书。此版本不内置 ACME/Cloudflare 申请或自动续期。TLS 由监测入口终止，官方内部 HTTP 只监听回环；官方安全响应头保留，HTTPS 会话 Cookie 添加 `Secure`。

如另外添加外部 HTTPS 反向代理，需向本服务 **HTTPS 回源并保留浏览器 Host**，让删除操作的同源校验仍匹配；本服务不直接信任客户端可伪造的 `X-Forwarded-Proto`。普通 HTTP 回源搭配外部 HTTPS 的删除操作未作为支持方案交付。

如需更改首次监听设置，可用：

```bash
sudo x-ui init --bind 127.0.0.1 --port 18080 --base-path /my-panel/
```

部署目录是实际的数据归属目录；安装命令只是创建指向该目录的管理入口，不复制数据到另一个位置。不要删除或移动正在使用的部署目录。同一文件系统内移动时先停止，再移动完整目录并重新运行 `install.sh`；跨磁盘、跨文件系统或跨服务器迁移请用 `backup` / `restore`，让日志文件标识一并转换。

如果目标机器已有原版 `/usr/local/bin/x-ui`，安装器会保留它并使用 `x-ui-tab`，所有示例命令相应换成 `x-ui-tab`。**同时存在旧原生面板时，应先停止旧服务或指定不同内部端口**；脚本不会自动卸载、迁移或覆盖原生部署。若旧 `x-ui` 在 `/usr/bin`，也应先检查 `command -v x-ui` 并明确使用本部署目录的 `./x-ui`。

## 实际提供的菜单

无参数运行 `x-ui` 显示 20 项管理功能：

| 功能 | 行为 |
| --- | --- |
| 启动、停止、重启、状态 | 管理本部署的 Compose 服务，状态含 Docker 健康检查 |
| 最近日志 | 查看受 Docker 轮转管理的面板/监测输出 |
| 重启 Xray | 调用官方重启接口，保留配置 |
| 重置用户名/密码 | 使用官方 CLI，更新本地凭证文件 |
| 修改访问路径 | 官方路径与监测入口同步；支持非根路径 |
| 修改公开面板端口 | 修改监测同源入口，入站端口保持独立 |
| 查看当前设置 | 地址、版本、7/35 天保留期、数据及凭证文件位置 |
| 导入 / 关闭 HTTPS | 使用已有证书，不修改官方内核 |
| 启用 / 禁用自动重启 | `unless-stopped` / `no`；宿主机 Docker 服务仍需设置开机启动 |
| 更新镜像 | 使用已导入的本项目镜像；自动一致性备份、健康检查和失败恢复 |
| 备份 / 恢复 | 两个数据库、配置、凭证、证书、Core 与日志一起处理 |
| 卸载容器 | 删除本部署容器，保留数据和备份 |
| 关闭两步验证 | 忘记二步验证时通过官方 CLI 重置 |
| 重建采集 Token | 重新生成服务器端凭证，不更改浏览器登录账号 |

系统防火墙、SSH、BBR、宿主机 systemd、Fail2ban、PostgreSQL 和系统卸载等不属于此容器菜单。用户常用的 VLESS + TCP + REALITY 由官方面板配置。容器没有授予 `NET_ADMIN`、特权模式或 Docker socket；需要系统网络管理能力的其他协议/功能不在本包验收范围。

## 网络与持久化

Linux 使用 `network_mode: host`，新建 Xray 入站端口可以直接生效，无需逐个添加 Docker 端口映射。访问控制由宿主机/云服务商防火墙负责。不要与其他程序占用同一个端口。

| 默认端口 | 监听范围 | 用途 |
| --- | --- | --- |
| 18080 | `0.0.0.0`，可在 init 改为回环 | 官方面板与流量监测的公开入口 |
| 12053 | `127.0.0.1` | 官方内部 HTTP |
| 62789 | `127.0.0.1` | Xray 内部 API / 日志重开 |
| 11111 | `127.0.0.1` | Xray 指标 |
| 自定义入站端口 | 按官方入站设置 | VLESS 等代理连接 |

初始化多个实例时同时指定 `--project-name`、`--port`、`--panel-port`、`--api-port`、`--metrics-port`，并使用独立部署目录。订阅服务首次默认关闭，可在官方界面按需配置订阅监听端口。

```text
部署目录/
├── x-ui / install.sh / compose.yaml / .env
├── image.tar.gz / bundle.json / SHA256SUMS
├── docs/ / licenses/ / sources/
├── data/
│   ├── panel/           # 官方数据库
│   ├── monitor/         # 监测数据库和采集检查点
│   ├── state/           # 部署配置、采集 Token、首次/重置登录凭证
│   ├── core/            # 官方 Core、Geo 文件与运行配置
│   ├── logs/            # Xray 访问日志
│   ├── certs/           # 导入证书和私钥
│   └── tmp/            # 容器工作临时目录
├── backups/            # 私密备份和恢复前数据
└── .cache/             # 管理工具缓存与恢复暂存
```

两个程序由容器内轻量 Python 进程管理，收到停止信号先停止监测再退出面板、保存流量并关闭 Xray。任何一个主进程异常退出都会使容器退出，由 Docker 重启策略处理。日志/数据库均在挂载目录，容器重建不会删除。

固定内部端口、日志路径由部署配置管理。公开端口和 HTTPS 请使用 `x-ui` 修改；不要在官方网页把内部监听改为公网或给内部 HTTP 设置证书。网页更改访问路径会在约 5 秒内同步监测入口。更改其余部署配置后执行 `x-ui restart`。

`data/state/deployment.json` 集中控制明细 7 天、汇总 35 天与 10 秒采样，校验限制明细不超过 7 天、汇总不超过 35 天。`state/monitor.json` 是派生文件，不直接编辑。容器启动只调整持久 Xray 模板中的监测日志/API/指标端口，不覆盖入站和用户路由。

## 更新、备份和恢复

```bash
sudo x-ui backup
sudo x-ui load /path/to/new-bundle/image.tar.gz
sudo x-ui update x-ui-tab:3.9.0-1.0.2-amd64  # 示例；填写实际已构建导入的版本
sudo x-ui restore /absolute/path/to/backups/x-ui-....tar.gz --yes
```

`update` 会停机做备份，重建容器并等待健康状态；启动失败则恢复备份和旧镜像。升级使用**本项目整合镜像**，不能直接换成官方单独的 3x-ui 镜像。禁止数据库直接降级；需要回退时恢复与旧版本配套的备份。健康状态验证服务可达性，未知官方布局仍需做浏览器适配验收，独立监测页面继续保留。

镜像更新不自动替换宿主机的管理脚本。新包若包含菜单更新，核验新包校验和后，将其 `x-ui` 和 `install.sh` 复制到**原部署目录**并保留执行权限；Compose 配置变化按对应版本说明合并。不要在新包目录重新 `init` 后把它当作旧数据实例。

普通 `backup` 完成后恢复原运行状态。备份短暂停止代理连接以保证两个数据库与日志检查点一致，包含密码、Token、证书及私钥，默认文件权限 `600`。`restore` 先验证路径与格式并检查对应镜像已导入；覆盖前再做备份，原数据另保存在 `backups/replaced-data-*`。这些手工备份不会随 7/35 天自动清理，定期转移/清理以控制磁盘。

备份还记录每个访问日志的 SHA-256 和已提交读取位置。恢复到另一目录/服务器会改变文件标识，恢复器在服务启动前核验日志内容并重新绑定检查点，保留未读尾部与不完整行，防止旧日志重复计数。缺少这份清单或日志不匹配的备份会拒绝自动恢复。管理命令需在持有这些数据的 Linux 宿主机上使用本地 Docker daemon，不支持远程 Docker context。

同一官方发行包下，在网页更新的 Xray/Geo 文件保存在 `data/core`，容器重建后保留。升级到不同官方发行包时 Core/Geo 采用新镜像附带版本，原版已包含在更新前备份里。监测规则随本项目版本发布。

## 卸载和排障

```bash
sudo x-ui status
sudo x-ui logs --tail 100
sudo x-ui logs --follow
sudo x-ui restart-xray
sudo x-ui rotate-token
sudo x-ui remove --yes        # 删除本部署容器；保留 data 和 backups
```

移除管理命令前可用 `readlink -f /usr/local/bin/x-ui` 确认它指向本部署；只删除该符号链接。彻底删除数据是独立手工操作，请先核对部署目录与备份。没有执行 `docker system prune` 或删除无关镜像的步骤。

如 `init` 提示端口占用，使用 `ss -ltnp` 查找冲突程序，或重新指定端口。容器健康失败先查看日志；登录失败通过 `x-ui credentials` 重置。若网页单独修改过密码，旧 `credentials.json` 不会自动获取新明文密码。官方 API Token 失效时用 `rotate-token` 恢复。

## 本机构建与验证

本节是维护者的可选开发流程，服务器安装用户使用已经发布的包即可。源码在 `deploy/docker/`，构建/打包脚本在 `scripts/`，原生开发实例见 [本地开发说明](local-development.md)。本机 WSL 使用项目目录内的独立 Docker daemon，其所有镜像层、构建缓存、socket、日志位于 `cache/docker-engine/`，不改动默认 daemon 的已有镜像。ext4 工作磁盘文件容量为 8 GiB，在当前 Windows 挂载盘上也可能占用完整 8 GiB；它是开发构建缓存，不随交付包上传服务器。

```bash
source scripts/env.sh
bash scripts/docker-engine.sh start
eval "$(bash scripts/docker-engine.sh env)"
# 首次可从默认 daemon 导出已有 alpine:3.22 到 cache/docker-images 再导入；
# 或直接在此项目 daemon 内 docker pull alpine:3.22。
bash scripts/build-docker.sh
python3 scripts/test-docker.py
```

验收保留自己的数据和报告；重复运行时指定新的目录名，例如 `DOCKER_TEST_FIXTURE=docker-acceptance-2 python3 scripts/test-docker.py`，且确保上一轮测试容器已停止。验收全程使用 28080/28081 等独立端口。

构建使用官方 Linux 发行包 SHA-256、单独的明确文件清单和静态 Go 二进制；`sources/` 提供本项目源码与对应官方源码地址，`licenses/` 保存上游及规则许可。新版本通过 `PANEL_VERSION` / `MONITOR_VERSION` 显式指定。未来官方版本发布后仍需实际验证其 CLI、持久配置和页面布局。

2026-10-12，Windows Defender 将官方完整源码压缩包标记为 `Trojan:Win32/Qwexlafiba!rfn` 并隔离；未确认是否误报。没有恢复隔离文件、关闭防护或添加排除项。该源码压缩包不参与构建或交付；仅使用已校验的 Linux 发行包和两份所需的纯文本配置/许可文件，记录官方源码版本地址。

资源评估见 `resource-assessment.md`。Compose 默认内存上限 640 MiB，Go 内存目标每进程 192 MiB、`GOMAXPROCS=1`，适用于先前的 1 核 / 1 GB、最多 5 台设备场景；应为宿主机、Docker 及文件缓存留余量。35 天容量取决于连接记录数量，不是月代理传输总字节数。Docker 镜像与手工备份需另计磁盘；构建镜像应在开发机执行。
