# 初版 CI/CD 验证记录

日期：2026-10-24。安全修复基线为 `c9b16430c33964a6bd9c61718cc3cbf8ce3be815`，
本记录验证该基线加本次 CI/CD 工作区修改。所有服务验证均为本机隔离测试。

| 检查 | 实际结果 |
| --- | --- |
| Shell/Python/任务 XML | Bash 语法、Python 编译、Jenkins 多分支 XML 解析通过 |
| Jenkinsfile | Groovy 2.4.21 语法解析通过；Jenkins 服务端 Declarative 模型校验未执行 |
| 交付回归 | 8 项通过，覆盖包校验、ELF 架构、拒绝 tar 符号链接、成功备份升级、失败恢复二进制且保留新写入、schema 变化阻止不安全回退 |
| Docker verify target | 实际构建通过；Go 1.27.3 / Node 22 / Debian bookworm，Linux arm64 |
| Go 回归 | 279 个测试通过（含子测试），3 个测试包通过；排除手工 `TestRttysStress` |
| 静态与依赖检查 | `go mod verify`、`go vet` 通过；`govulncheck` 为 0 可达/导入包漏洞，1 个未使用模块级告警；npm 审计全部为 0 |
| 前端 | `npm ci` 和 Vite 生产构建通过 |
| 本地 E2E | 真实 HTTPS/TLS/WebSocket 通过；HTTP/socket 退出码均为 0，第二写入实例被拒绝，重启持久化通过 |
| 可归档摘要 | `RTTY_RUN_SUMMARY` 实际写出可解析的独立 JSON；未将密码、私钥或测试数据库复制到 `out/verify` |
| 部署健康检查 | 使用实际本地 rttys 服务验证 TLS CA、登录页和未登录 `/api/me` 的 401，通过 |
| Docker runtime target | amd64、arm64 两种镜像实际构建通过；image architecture、revision、二进制 ELF 和包 SHA256 均通过 |
| 镜像运行 | 两种镜像均以 UID/GID 65532、无网络、只读根文件系统、无 capabilities 运行 `--version` 成功；amd64 使用本机 Docker 的仿真支持 |

本地证据在被 Git 忽略的 `out/verify/`、`out/release/`、`out/local-check/`。
测试镜像为 `registry.jingan.com:32008/ja/ja-rttys:local-ci-c9b1643` 及 `-arm`，
仅在本机 Docker 中，两个 `image.json` 均为 `pushed=false`；不是已发布版本。

114 的历史 Jenkins 入口 `http://172.21.30.114:3214` 本机直连超时，经代理返回 502。
因此本次没有创建/修改 Jenkins 任务，没有验证 105 实际执行节点、registry 登录推送或 systemd 远端升级。
待连接恢复后，按 [接入说明](README.md) 导入任务并先保持两个发布/部署参数关闭，
核实 Jenkins 服务端模型校验和首轮验证/出包，再启用所需交付阶段。

没有部署到 204、105 或其他业务环境；实机业务验收、浏览器验证和线上安全复扫未执行。
