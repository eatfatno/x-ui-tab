# 发布到 eatfatno/x-ui-tab

仓库网页：https://github.com/eatfatno/x-ui-tab 。Git 远程地址：https://github.com/eatfatno/x-ui-tab.git 。两者指向同一个仓库。

源码和正式 [Release v1.0.2](https://github.com/eatfatno/x-ui-tab/releases/tag/v1.0.2) 于 2026-10-15 更新，两个附件已上传并通过 GitHub SHA-256 核对。安装用户直接使用第 3 节；第 1、2 节保留为维护者发布流程，不需要重复创建已有版本或移动标签。

## 1. 上传整理好的源码

先在 WSL 项目根目录执行 `source scripts/env.sh`，再执行 `python3 scripts/prepare-github.py`。脚本只选择源码、配置模板、文档和许可证；排除依赖安装目录、数据库、凭证、日志、缓存和构建结果，并对常见凭证格式及本机私有配置中的敏感值进行检查。完整待发布文件清单保存在 `artifacts/github/source-manifest.json`。

生成的 Windows 目录：

```text
D:\work\codexworkspace\x-ui-tab\artifacts\github\source
```

### 推荐：使用 Git 上传

在 Windows PowerShell 执行：

```powershell
Set-Location D:\work\codexworkspace\x-ui-tab
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\publish-github.ps1
```

脚本将整理后的目录初始化为独立 Git 仓库并生成首个提交，远程固定为 `eatfatno/x-ui-tab`，上传到 `main`。Git Credential Manager 可能打开浏览器，请登录 `eatfatno` 完成授权，不需要把密码或访问 Token 发给其他人。脚本不会强制推送；远端已经有内容时 Git 会阻止覆盖，按错误信息处理。

首次上传后通过 GitHub 网页创建 Release。后续源码修改应在正常 Git 工作流程中管理；再次生成发布快照前，脚本会拒绝覆盖已有 Git 仓库。

### 网络不支持 Git 时：通过网页上传

空仓库页面点击 **uploading an existing file**。从上述 `source` 目录上传实际文件和文件夹，不能把 ZIP 当成源码上传，也不能多套一层 `source` 文件夹。GitHub 网页每次最多 100 个文件，建议分两次：先上传 `apps` 文件夹，再上传 `deploy`、`docs`、`scripts` 和根目录的 README、LICENSE、THIRD_PARTY_NOTICES、`.gitignore`、`.gitattributes`。每批完成后点击 **Commit changes**。第二批入口在 **Add file → Upload files**。

如果网页隐藏点号文件，在 Windows 文件资源管理器开启显示隐藏项目，或使用 Git 上传。`apps` 若增长超过 100 个文件，也需要继续拆分上传。

## 2. 发布成品安装包

1. 打开仓库的 **Releases**，点击 **Create a new release**，或直接进入 https://github.com/eatfatno/x-ui-tab/releases/new 。
2. 在 **Choose a tag** 中输入 `v1.0.2`，选择创建新标签；**Target** 选 `main`。标签应指向包含在线安装脚本的源码提交。
3. 标题填写 `v1.0.2 · 3x-ui 3.10.2 + 流量监测`。
4. 说明内容使用 `docs/releases/v1.0.2.md`。
5. 上传下列两个附件（不要改文件名）：

   ```text
   artifacts/docker/x-ui-tab-3.10.2-1.0.2-linux-amd64.tar.gz
   artifacts/docker/x-ui-tab-3.10.2-1.0.2-linux-amd64.tar.gz.sha256
   ```

6. 保持正式版本，**不勾选 pre-release**，点击 **Publish release**。仅保存草稿不能供公开安装脚本下载。

源码与发布包来自同一项目。v1.0.2 升级到官方 3x-ui 3.10.2 / Xray 26.10.10，保留前版首次初始化竞态修复，增加新版本适配、跨版本数据库恢复验收及官方 API 分段下载。旧 v1.0.1 标签和附件保留。

## 3. 服务器安装

正式部署使用 Linux x86_64 / amd64 宿主机上的 Docker Engine + Compose v2，无需 Windows 或 WSL。Ubuntu、CentOS Stream 等发行版的依赖与配置条件见 [系统环境与依赖](docker-deployment.md#系统环境与依赖)；WSL 仅是本项目使用的开发与验证环境。

Ubuntu 22.04/24.04 服务器执行，脚本可安装缺少的依赖：

```bash
curl -fsSL https://raw.githubusercontent.com/eatfatno/x-ui-tab/v1.0.2/scripts/install-online.sh | sudo bash -s -- --version v1.0.2
```

CentOS Stream 等其他发行版先准备 Docker、Compose v2、Python 3.10+ 等依赖，以及适用的 SELinux 数据目录配置，再执行：

```bash
curl -fsSL https://raw.githubusercontent.com/eatfatno/x-ui-tab/v1.0.2/scripts/install-online.sh | sudo bash -s -- --version v1.0.2 --no-install-deps
```

若 `raw.githubusercontent.com` 连接被重置或超时，可使用 GitHub 官方 API 的原始文件响应：

```bash
curl -fsSL -H 'Accept: application/vnd.github.raw+json' 'https://api.github.com/repos/eatfatno/x-ui-tab/contents/scripts/install-online.sh?ref=v1.0.2' | sudo bash -s -- --version v1.0.2
```

两个入口都固定为 `v1.0.2`，脚本内容相同，不经过第三方镜像，不需要 GitHub 凭证。其他发行版使用 API 入口时也加 `--no-install-deps`。备用入口仍需要访问 GitHub API 和 Release 附件 CDN。如果 `curl` 尚未安装，Ubuntu 先执行 `sudo apt-get update && sudo apt-get install -y curl ca-certificates`。

默认目录 `/opt/x-ui-tab`，首次账号密码文件 `/opt/x-ui-tab/data/state/credentials.json`。使用 `sudo x-ui` 管理；原来已有其他 x-ui 时使用 `sudo x-ui-tab`，完整路径 `/opt/x-ui-tab/x-ui` 始终可用。Web 密码修改后请以实际设置为准。

默认脚本入口需要访问 `raw.githubusercontent.com`；备用入口使用 `api.github.com`。安装过程还需要 `api.github.com`、`github.com` 及 GitHub Release 附件 CDN。网络无法访问时可下载两个 Release 附件并按照 [离线部署说明](docker-deployment.md) 手工安装。

已有 Docker 的主机会保留现有运行环境；Docker daemon 未启动时脚本报错并提示先启动。只给未安装 Docker 的受支持 Ubuntu 主机安装官方 Docker 软件包；检测到已有其他容器运行时会停止自动处理，避免替换系统组件。镜像导入使用本地 Docker 服务，和管理脚本相同，不支持远程 Docker context。

重跑一键脚本不会覆盖已经初始化的 `data` 或 `.env`。首次启动失败但已生成数据时，通过 `/opt/x-ui-tab/x-ui status`、`logs`、`start` 排障，不要清空目录重装。

## 验证范围

`scripts/test-online-installer.py` 检查哈希不符、异常归档、路径越界、链接、版本/架构不符及已有文件保护。`scripts/verify-online-install.py` 使用本地 GitHub 下载模拟和实际 Docker 包，完成真实解压、导入、启动、登录及重复安装保护；所有测试目录与 Docker 存储均在本项目内。

v1.0.2 使用 GitHub 官方资产 API 取得 HTTPS 附件地址，大包以 8 个并发请求、每段 1 MiB 下载；每段验证 Content-Range 和长度，网络中断最多尝试 3 次，全部下载后再检查整包及包内 SHA-256。下载暂存目录位于部署目录，成功或失败后清理。不需要 GitHub 登录，也不经过第三方下载镜像。

安装边界测试新增不完整分段重试、错误 Content-Range 和异常下载主机检查，共 17 项。真实发布后的在线安装报告保存在 `artifacts/reports/public-online-install.json`；命令 `scripts/verify-public-install.py` 检查公开脚本、Release、实际安装、登录、浏览器和重复安装保护。支持用 `--offline-bundle` 单独验证离线流程，两者独立记录，不相互替代。

v1.0.1 历史公开下载与离线验收已通过；当时单连接在线安装在 600 秒内未完成。该历史失败是此次下载改进的依据。新版的测试结果和升级步骤见 [v1.0.2 升级说明](upgrade-v1.0.2.md)。

干净 Ubuntu 主机自动安装系统依赖尚未实际验证。WSL 使用已有 Docker/Compose/Python，未修改宿主机 apt 软件源。对网络不能稳定下载 Release 附件的服务器，使用前文的离线部署方式。

当前 `x-ui update` 接受已导入镜像，自动备份、检查启动状态并在失败时恢复；从 GitHub 在线升级及 Actions 自动构建不包含在此次安装入口交付中。
