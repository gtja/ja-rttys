# Mender 应用升级接入

当前提供租户管理员使用的“应用升级”入口。rttys 后端校验租户、设备权限及版本归属，再调用 Mender；浏览器不会收到 Mender 管理凭据。

## 部署配置

通过 `--mender-config /absolute/private/mender.json` 加载配置。文件应仅由服务账号读取，不能提交到 Git。

```json
{
  "url": "https://mender.example.com",
  "ca_file": "/absolute/private/mender-ca.crt",
  "username": "service@example.com",
  "password": "REPLACE_PRIVATELY",
  "artifact_prefixes": {"TENANT_INTERNAL_ID": "tenant-specific-prefix-"},
  "bindings": {
    "RTTYS_INTERNAL_DEVICE_ID": {
      "tenant_id": "TENANT_INTERNAL_ID",
      "mender_id": "VERIFIED_MENDER_DEVICE_UUID"
    }
  }
}
```

租户内部编号和 rttys 设备内部编号为 64 字符，不能用客户端 `-I` 替代。版本前缀必须互不重叠。绑定必须在核对设备公钥身份后由管理员配置；客户端不能自行指定归属。配置修改后重启服务生效。

版本先由管理员导入 Mender，并使用对应租户前缀命名。当前没有自助上传和自动绑定功能。签名校验与失败回滚必须由设备端 Mender 配置和 Update Module 实现；服务端接口接通并不代表这两项已验证。

## 验证与当前限制

2026-10-27 已部署到 204，rttys 面板端口 18446，Mender 端口 18448。已完成真实服务上的登录、匿名拒绝、管理员访问、操作员及只读账号拒绝、未配置租户隔离检查。

重放只读验收：

```sh
python3 scripts/ota-smoke.py --fixture /private/test-access.json --ca /private/rttys-ca.pem --output /tmp/ota-smoke.json
```

此脚本针对早期“设备尚未绑定”的部署阶段；173 已接入后，其中空设备断言不再适用。现已采用实际发布及业务健康检查链路验收，记录见下文。

174 未修改。用户改用新盒子 172.21.0.173 后，已通过原安装包部署独立设备 nvidia-173，完成真实终端、上传下载及跨租户拒绝验证。

173 原 Mender 配置 JSON 不完整，已备份到 `/opt/ja-mender-backups/20261027-101622` 后修复。移除跳过证书检查的启动参数，安装后台 CA 与制品签名公钥，并核对设备公钥后授权及绑定。

通过 rttys 发布签名演示应用 v2，Mender 成功完成，健康接口返回 v2；再发布 broken-v3，健康检查失败，Mender 报告 failure，自动回滚到 v2，配置保持不变。演示模块为 `scripts/ja-ota-demo-module`，只管理 `/opt/ja-ota-demo` 和 `ja-ota-demo.service`。初次模块目录权限问题已修复，失败记录保留。真实业务程序尚未替换或验收，浏览器点击交互尚未验收。

可核验工件位于本地 `/Users/tensei/memo/tenseidays/2025/2026/2026-10/10-27/rttys-173/`，包含重放说明、终端及文件结果、OTA任务状态、配置与版本检查、签名与回滚日志。

发布任务使用请求编号防止同一请求重试造成重复部署。状态为 unknown 或 creating 时，应先检查 Mender 后台，不能另建任务盲目重发。设备软删除会阻止新发布，但不会取消已经被 Mender 接受的任务；当前尚无取消入口。版本列表和设备绑定适合当前单设备试点，大规模分页与自动注册管理尚未验收。

本次 204 部署前备份：`/opt/rttys-tenant-poc/backups/pre-ota-20261027-094402/`，包含旧二进制和 systemd 服务文件。回退时恢复二者，执行 daemon-reload 后重启 rttys-tenant-poc。新增 ota_jobs 表可保留，不影响旧版本。
