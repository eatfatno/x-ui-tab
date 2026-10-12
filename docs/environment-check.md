# WSL 旧 x-ui 检查与清理记录

检查与执行日期：2026-10-08（Asia/Shanghai）。发行版：`Ubuntu-24.04`，WSL 2。本次范围只包含用户已授权删除的旧 x-ui 部署，不备份、不迁移旧访问记录。

## 清理前确认

旧部署并未卸载，检查时仍在运行：

| 对象 | 实际证据 |
| --- | --- |
| `x-ui-custom.service` | `active`、`enabled`，服务文件 `/etc/systemd/system/x-ui-custom.service` |
| `x-ui.service` | 指向上述服务文件的别名 |
| 服务命令 | `ExecStart=/home/john/x-ui/start.sh`，工作目录 `/home/john/x-ui`，运行用户 john |
| 旧面板进程 | PID 203，`/proc/203/exe` 指向 `/home/john/x-ui/x-ui` |
| 旧 Xray 进程 | PID 362，`/proc/362/exe` 指向 `/home/john/x-ui/bin/xray-linux-amd64` |
| 监听 | 面板 2053、2096；Xray 127.0.0.1:11111、127.0.0.1:62789 |
| `/home/john/x-ui` | 约 345 MB，旧运行程序、数据库、配置及访问日志 |
| `/etc/x-ui` | 约 236 KB，旧 `x-ui.db` |
| `/var/log/x-ui` | 旧日志目录 |
| `/opt/3x-ui-custom` | 约 153 MB，`go.mod` 模块为 `github.com/mhsanaei/3x-ui/v3` |
| `/home/john/.cache/3x-ui-traffic-monitor` | 约 2.9 GB，旧源码、工具链、构建与测试缓存 |
| `/usr/bin/x-ui`、`/usr/local/bin/x-ui` | 两个包装脚本都将 `XUI_MAIN_FOLDER` 指向 `/home/john/x-ui`、服务名指向 `x-ui-custom` |
| 测试容器 | `1e4fb806f36e` / `traffic-monitor-systemd-test-3f50465b00ab`，状态 Exited，没有外部挂载 |
| 专用测试镜像 | `traffic-monitor-systemd-test:local`，ID 前缀 `2120668ad718` |

所有待递归删除目录均确认是普通目录，`realpath` 与固定允许清单完全一致。服务别名与启用链接均确认只指向已知旧服务文件。

## 执行动作

执行了项目脚本 `scripts/cleanup-legacy.sh --dry-run`，核验全部通过后执行 `--execute`。

1. 停止并禁用 `x-ui-custom.service`，验证旧二进制进程退出。
2. 删除旧主服务、`x-ui.service` 别名、多用户目标启用链接及两个旧命令入口，重新加载 systemd。
3. 删除允许清单中的五个旧目录，不跨越嵌套挂载，不扫描删除其他目录。
4. 删除已确认的专用测试容器，删除对应镜像标签及不再使用的镜像层。
5. 未执行 Docker 全局 prune，未删除任何通用基础镜像或无关容器。

该清理脚本默认为只读 dry-run，且必须从固定项目目录执行。它在执行前重新验证固定目录、服务、包装脚本及专用容器身份；身份改变时拒绝删除。

## 清理后独立验证

| 检查项 | 结果 |
| --- | --- |
| `x-ui-custom.service` | `LoadState=not-found`、`ActiveState=inactive` |
| `x-ui.service` | `LoadState=not-found`、`ActiveState=inactive` |
| systemd 服务目录中的 x-ui 单元 | `/etc/systemd/system`、`/usr/lib/systemd/system`、`/lib/systemd/system` 均未找到 |
| 指向旧部署或旧源码目录的进程 | 未找到 |
| 原端口 2053、2096、11111、62789 | 均无监听（本记录为新基线部署前的状态） |
| 七个旧目录/命令路径 | 全部不存在，包括符号链接检查 |
| 专用测试容器 | 不存在 |
| 专用镜像标签 | 不存在 |
| 其他容器 | 九个原有 `suricata-nsm-*` 容器保留，状态与清理前一致 |

**结论：本次确认范围内的旧 x-ui 部署已卸载干净，可以在项目目录建立全新的官方运行基线。** 本报告不代表对整台 WSL 任意路径做过数据取证式扫描；保留了未确认属于旧项目的环境内容。

新部署、采集、界面和升级验证结果另行记录，不属于此旧环境清理结论。
