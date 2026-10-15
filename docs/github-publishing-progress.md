# GitHub 发布与交付进度

更新日期：2026-10-15（Asia/Shanghai）。用户已恢复项目工作；此前暂停已结束。

## 已交付

- 公开源码仓库：https://github.com/eatfatno/x-ui-tab 。
- 正式 Release：https://github.com/eatfatno/x-ui-tab/releases/tag/v1.0.1 。
- 发布标签源码提交：`ccbfe51b9d2f45349e1eca5111a4f8d6092a0469`；标签和已发布附件不变，后续文档与验收脚本更新到 main。
- 部署包：`artifacts/docker/x-ui-tab-3.9.0-1.0.1-linux-amd64.tar.gz`，177,133,451 字节。
- SHA-256：`fe43e21f97e0f9b96033f1e372fc0ed0b66eae18d7552867499d9c5fa5323a5a`。
- Docker 镜像、20 项 Linux 管理菜单、备份恢复、升级失败恢复和已有功能验收均已完成。部署说明见 `docker-deployment.md`。

## 本次补齐的验收

从 GitHub 官方 API 读取已发布脚本，从 Release 附件 CDN 实际下载完整安装包。没有使用第三方镜像、GitHub 凭证或本地下载模拟。整包 SHA-256 同时匹配公开校验文件、GitHub digest 和原交付包。

当前网络直连 raw 域名被重置，在线单连接大文件下载在 600 秒测试期限内未完成。使用 HTTPS 分段下载取回全包后，在 WSL 项目专用 Docker 环境实际安装，7 项安装/接口检查、4 项 Chromium 页面检查通过。新凭证、会话校验、7/35 天保留、嵌入页、分钟筛选、删除弹窗、手机/刷新、独立页及重装保护均正常。测试容器已移除。

报告：

- `artifacts/reports/public-release-download.json`：真实下载、大小、哈希。
- `artifacts/reports/public-bundle-install.json`：公开包安装与页面验收。
- `artifacts/reports/public-online-install.json`：单连接测试超时，保留真实失败结果。
- `artifacts/reports/github-release-readiness.json`：最新交付状态。
- `artifacts/reports/github-release-publication.json`：发布来源与附件记录。

一键安装和官方 API 备用入口见 `README.md`、`github-publishing.md`；网络较慢时可把 Release 附件手工传到服务器离线安装。

## 验证边界

本阶段交付与公开下载后的安装验收已完成。干净 Ubuntu 的 apt 自动准备依赖未实际执行；本机已有 Docker/Compose/Python。单连接在线完整流程未在当前慢网络通过，不能用分段下载加离线安装代替这一结果。没有部署到未指定的实际服务器；ARM、GitHub 在线升级菜单和 Actions 自动构建不属于本次版本。

所有新增文件仍位于项目根目录。没有修改 WSL 网络、系统防火墙、代理或软件源，没有关闭 WSL 或改动 OpenCode。源码发布工作目录仍为 `artifacts/github/source`；私有凭证、访问历史、测试日志和缓存不上传。
