# 发布到 eatfatno/x-ui-tab

仓库网页：https://github.com/eatfatno/x-ui-tab 。Git 远程地址：https://github.com/eatfatno/x-ui-tab.git 。两者指向同一个仓库。

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
2. 在 **Choose a tag** 中输入 `v1.0.1`，选择创建新标签；**Target** 选 `main`。标签应指向包含在线安装脚本的源码提交。
3. 标题填写 `v1.0.1 · 3x-ui 3.9.0 + 流量监测`。
4. 说明内容使用 `docs/releases/v1.0.1.md`。
5. 上传下列两个附件（不要改文件名）：

   ```text
   artifacts/docker/x-ui-tab-3.9.0-1.0.1-linux-amd64.tar.gz
   artifacts/docker/x-ui-tab-3.9.0-1.0.1-linux-amd64.tar.gz.sha256
   ```

6. 保持正式版本，**不勾选 pre-release**，点击 **Publish release**。仅保存草稿不能供公开安装脚本下载。

源码与发布包分别来自同一项目。在线安装器及发布操作工具位于源码仓库；v1.0.1 Docker 包修复了慢磁盘首次启动时，健康检查提前创建空数据库的问题；面板和监测功能沿用已经验证的实现。旧 1.0.0 包保留在本地，GitHub 首次发布使用修复后的 1.0.1。

## 3. 服务器安装

完成源码及 Release 发布后，Ubuntu 22.04/24.04 x86_64 服务器执行：

```bash
curl -fsSL https://raw.githubusercontent.com/eatfatno/x-ui-tab/v1.0.1/scripts/install-online.sh | sudo bash -s -- --version v1.0.1
```

默认目录 `/opt/x-ui-tab`，首次账号密码文件 `/opt/x-ui-tab/data/state/credentials.json`。使用 `sudo x-ui` 管理；原来已有其他 x-ui 时使用 `sudo x-ui-tab`，完整路径 `/opt/x-ui-tab/x-ui` 始终可用。Web 密码修改后请以实际设置为准。

下载脚本需要访问 `raw.githubusercontent.com`、`api.github.com`、`github.com` 及 GitHub Release 附件 CDN。网络无法访问时可下载两个 Release 附件并按照 [离线部署说明](docker-deployment.md) 手工安装。

已有 Docker 的主机会保留现有运行环境；Docker daemon 未启动时脚本报错并提示先启动。只给未安装 Docker 的受支持 Ubuntu 主机安装官方 Docker 软件包；检测到已有其他容器运行时会停止自动处理，避免替换系统组件。镜像导入使用本地 Docker 服务，和管理脚本相同，不支持远程 Docker context。

重跑一键脚本不会覆盖已经初始化的 `data` 或 `.env`。首次启动失败但已生成数据时，通过 `/opt/x-ui-tab/x-ui status`、`logs`、`start` 排障，不要清空目录重装。

## 验证范围

`scripts/test-online-installer.py` 检查哈希不符、异常归档、路径越界、链接、版本/架构不符及已有文件保护。`scripts/verify-online-install.py` 使用本地 GitHub 下载模拟和实际 Docker 包，完成真实解压、导入、启动、登录及重复安装保护；所有测试目录与 Docker 存储均在本项目内。

本地测试不代表 GitHub Release 已发布。发布后的真实 URL、Git 身份验证以及干净 Ubuntu 主机自动安装系统依赖还需在对应环境确认。已有 Docker/Python 的 WSL 验证不会修改本机 apt 软件源。

当前 `x-ui update` 接受已导入镜像，自动备份、检查启动状态并在失败时恢复；从 GitHub 在线升级及 Actions 自动构建不包含在此次安装入口交付中。
