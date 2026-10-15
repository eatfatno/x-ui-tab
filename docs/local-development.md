# 本地开发与测试

本文记录本项目开发机上的 Windows + WSL Ubuntu 24.04 环境，供修改源码、构建和运行验收时参考。服务器部署使用 [Docker 部署说明](docker-deployment.md)，不需要 Windows、WSL 或下面的开发目录。

## 项目目录

当前 Windows 项目目录为 `D:\work\codexworkspace\x-ui-tab`，WSL 对应路径为 `/mnt/d/work/codexworkspace/x-ui-tab`。这些是本开发机的路径；在其他开发环境中应替换为实际项目目录。

代码在 `apps/traffic-monitor`，前端在其 `web` 子目录。官方包放入 `upstream/3x-ui`，工具链放入 `tools`，运行配置、数据、日志和缓存分别位于 `runtime`、`data`、`runtime/logs`、`cache`，测试结果在 `artifacts`。

## 访问原生开发实例

在 WSL Ubuntu-24.04 内运行后，Windows 浏览器访问：

- 嵌入页面：http://localhost:18080/panel/?monitor=traffic
- 独立页面：http://localhost:18080/traffic-monitor/

两个页面使用官方面板同一登录会话。此处为原生开发实例；Docker 部署的凭证位于其部署目录下的 `data/state/credentials.json`。

原生开发实例首次创建的随机登录密码保存在 `runtime/lab-state.json`，用户名为 `monitor-admin`。该文件和 `runtime/monitor/api-token` 是本机私有配置，不提交到 Git。首次登录后可以在原面板修改密码；运行验收脚本前需同步本地测试凭证。

PowerShell 启动、停止与状态查看：

```powershell
wsl -d Ubuntu-24.04 -- bash /mnt/d/work/codexworkspace/x-ui-tab/scripts/start.sh
wsl -d Ubuntu-24.04 -- bash /mnt/d/work/codexworkspace/x-ui-tab/scripts/stop.sh
wsl -d Ubuntu-24.04 -- python3 -B /mnt/d/work/codexworkspace/x-ui-tab/scripts/lab.py status
```

这些脚本仅管理本项目 PID，默认不安装系统服务、不修改 WSL 启动配置。验收用客户端以 `monitor-test-` 命名；测试实例的访问记录来自测试流量。

## 源码构建

所有构建和测试先加载项目环境，确保临时文件及缓存留在项目目录：

```bash
cd /mnt/d/work/codexworkspace/x-ui-tab
source scripts/env.sh
bash scripts/build.sh
python3 scripts/lab.py restart --only monitor
```

从干净目录准备官方包和 Go：运行 `python3 scripts/fetch-upstream.py`，随后用 Linux `tar` 把 `tools/downloads/go1.27.1.linux-amd64.tar.gz` 解压至 `tools`，把 `upstream/3x-ui/v3.9.0/x-ui-linux-amd64.tar.gz` 去除顶层目录解压至 `runtime/panel`。最后执行 `python3 scripts/lab.py init`、构建并启动。发行包和 Go 下载均做 SHA-256 校验。

Docker 镜像的本机构建步骤及开发专用 Docker daemon 说明见 [Docker 文档的构建与验证部分](docker-deployment.md#本机构建与验证)。服务器安装用户使用已经发布的部署包即可。
