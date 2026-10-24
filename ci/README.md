# Jenkins CI/CD 初版

流水线读取仓库根目录的 [Jenkinsfile](../Jenkinsfile)。参考已有的
`ja-alarm-mqtt-kafka-bridge` 和 `defenseAI` Jenkins 实现，复用源码检出、凭据 ID、
105 构建节点及版本追溯方式。本仓库保留单进程 SQLite、systemd 部署方式。

默认每次构建只验证和出包，`PUBLISH_IMAGE=false`、`DEPLOY_TEST=false`。
没有在本次改造中升级 204 服务，也没有修改其他服务或 Jenkins 任务。
本地 Docker 验证和服务端接入边界见 [验证记录](verification.md)。

## 流程

1. 在 `SCM_AGENT_LABEL`（默认 `built-in`）检出 Jenkins 选定的精确提交。
   用 `git archive` 生成源码快照及 SHA256，再传给 105，避免 105 直连 GitHub 的不稳定问题。
2. 在 `BUILD_AGENT_LABEL`（默认既有的 `ja-pipeline-builder-105`）解包并核对快照。
   节点需要 Bash、Git/检出节点、Python 3.8+、Docker 及 BuildKit；宿主机不需要安装 Go/Node。
3. 运行交付和回滚测试，然后在临时构建镜像内完成 `npm ci`、前端构建、完整 npm 审计、
   `go mod verify`、Go 回归、`go vet`、`govulncheck` 及真实 HTTPS/TLS/WebSocket 本地端到端验证。
   仅排除需要外部客户端的手工压力用例 `TestRttysStress`。任一检查失败均阻止出包及部署。
   每次构建传入独立 `CI_RUN_ID`，重新执行审计与回归。
4. 从同一提交交叉编译 Linux amd64、arm64，无需 QEMU；检查镜像架构、二进制 ELF 架构及源码 revision。
   出包包含二进制、示例配置、systemd unit、操作说明、manifest 和 SHA256。
   各架构 `image.json` 记录源码、二进制 SHA、镜像 ID；仅推送成功后记录 digest 和 `pushed=true`。
5. 如显式启用 `PUBLISH_IMAGE`，使用临时 `DOCKER_CONFIG` 登录、推送，并在结束时删除登录信息。
6. 如人工在 `master/dev` 显式启用 `DEPLOY_TEST`，使用对应 amd64 包升级 204 已有实例。

| 分支 | 检查与双架构出包 | 显式启用镜像推送时 | 部署 |
| --- | --- | --- | --- |
| master/dev | 是 | `registry.jingan.com:32008/ja/ja-rttys` | 仅人工启用后升级 204 |
| test/release | 是 | `registry.cn-hangzhou.aliyuncs.com/jingansi/ja-rttys` | 禁用 |
| 其他分支/PR | 是 | 禁用 | 禁用 |

版本包含服务版本、UTC 时间、分支、源码 SHA 前 12 位和 Jenkins 构建号。
arm64 镜像 tag 追加 `-arm`。这两个架构分别交付，不发布多架构 manifest 或 `latest`。
GitHub Actions 上游示例仍保存在 `docs/upstream-workflows`，未启用。

## Jenkins 接入

导入 [jenkins-multibranch.xml](jenkins-multibranch.xml)，任务名称建议 `ja-rttys`。
配置按每 5 分钟扫描，初始包含 `master dev test release codex/security-scan-20261024`；
只有存在 Jenkinsfile 的分支会创建子任务。默认参数会执行验证和出包。
本文件可在 Jenkins 可达时导入，不代表任务已经创建或运行成功。

复用历史工程中的凭据 ID，不复制任何凭据值：

| 凭据 ID | 类型 | 用途 |
| --- | --- | --- |
| `github-pat` | Username/password | GitHub Branch Source / HTTPS 检出 |
| `registry-jingan-com` | Username/password | master/dev 私仓推送 |
| `public-aliyun-registry` | Username/password | test/release 公仓推送 |
| `ssh-204` | Username/password | 可选的已有测试实例升级；账号需有该主机的 root 权限 |
| `ja-rttys-known-hosts` | Secret file | 可选部署所需，预先独立核验的 204 SSH 公钥 |

需要 Pipeline、Git/GitHub Branch Source、Credentials Binding；仅启用部署时需要 SSH Steps。
仓库和凭据的可写权限应限制在可信维护者；不应让不可信 PR 修改后读取发布凭据。
初版不配置邮件/钉钉发送，结果通过 Jenkins 日志和归档查看。

## 204 升级与回退

部署入口是 [scripts/ci-deploy.py](../scripts/ci-deploy.py)。仅更新已有的
`rttys-tenant-poc` 服务，不安装服务、不覆盖 unit/TLS/用户配置、不初始化新数据库。
运维先核对 [deploy-test.example.json](deploy-test.example.json) 中的实际路径，将配置放在
204 的 `/etc/rttys/ci-deploy.json`，root 所有、0600；备份目录 root 所有、0700。
`console_url` 的证书必须由 `tls_ca` 信任并匹配主机名，不使用跳过 TLS 校验的方式。

部署前校验包 SHA256、内层二进制 SHA256、源码提交、架构和 schema；
核对 systemd MainPID 运行的是配置中的原二进制，并实际打开了指定数据库。
目标目录使用 `flock` 锁，避免不同分支同时升级同一实例。
停止单实例后备份旧二进制和 SQLite，再原子替换二进制并启动。
验证 `/proc/<MainPID>/exe` 的 SHA256、HTTPS 登录页面及未登录 `/api/me` 的 401 边界。

验收失败会恢复旧二进制并重新验证，流水线仍报告失败；新产生的数据库写入不会被自动覆盖。
初版仅支持 schema v2 → v2 升级。若新程序意外修改 schema，保持服务停止并报告回滚失败，
由运维使用保存的数据库和旧二进制人工恢复，避免旧程序读写不兼容结构。
备份和结果回执保留在目标机，数据库、密码和私钥不进入 Jenkins 归档。

升级期间终端/WebSocket 会断开；本次 Cookie 调整需要用户重新登录。
上述验收仅覆盖进程、版本、TLS 和未登录边界，设备业务及浏览器验收、线上安全复扫仍需另行执行。

## 本地验证

```sh
bash -n ci-package.sh scripts/ci-verify.sh
python3 -m unittest discover -s tests/ci -p 'test_*.py' -v
# Linux Docker 节点；不会推送或部署
SOURCE_COMMIT="$(git rev-parse HEAD)" CI_RUN_ID="local-$(date +%s)" bash scripts/ci-verify.sh
SOURCE_COMMIT="$(git rev-parse HEAD)" PACKAGE_VERSION=local-check TARGET_ARCH=amd64 bash ci-package.sh
```

构建与测试证据在 `out/`；Git 和 Docker 上下文均排除该目录。
源代码审计与业务回归的已有结果见 [扫描整改说明](../README-TENANT.md#security-scan-remediation-2026-10-24)。
Jenkins 服务端语法验证、实际节点镜像构建、registry 推送及 CD 需要在目标网络可达后执行。
