# 一个盒子、一个编号、一个 Token

平台注册一次物理设备，获得一个设备编号与 Token。5G、1.4G 客户端共用 Token；客户端 `-I` 可以不同或变化，传输层以 `group: link:5g`、`group: link:14g` 区分连接。这是客户端原有协议字段，不需要升级客户端二进制；它不是租户名称或授权分组。

服务端先通过 Token 找到设备和租户，不再将客户端 `-I` 与平台登记编号绑定；登记编号仍保留供管理与确认操作使用。只有认证通过后才使用链路标记。允许 default、5g、14g、test，每个设备每种链路最多一个连接；未知 link: 标记被拒绝。未使用新标记的旧客户端继续按 default 链路工作。

平台设备列表保持一个条目，展示在线链路并可选择操作链路。终端、文件管理和 Web 代理支持 `?link=5g` 或 `?link=14g`。明确选择已离线链路时不会偷偷改走其他链路。未选择时新操作依次优选 5g、14g、default、test。已经打开的会话固定到建立时的连接，链路断开即关闭，不宣称无缝切换。

设备停用、软删除、Token 重置会撤销该设备全部链路及其会话；旧 Token 在全部链路失效。权限仍按物理设备及其分组判定。巡检与 OTA 以物理设备为目标，不重复执行多条链路。

在线历史按连接保存链路字段。设备在线数量按物理设备去重；链路上线次数、累计在线时长分别计数，多条链路同时在线不会被误写成单条设备在线时长。已有记录的链路默认 default。新增字段兼容旧版本回退读取。

## 现场配置

启动入口仍为 `/home/jingansi/apps/bin/startRtty.sh`，人工只编辑 `/home/jingansi/apps/conf/rtty-box.conf`，样例在 edge-manager 的 `script/rtty-box.conf.example`。

同一配置里填写 DEVICE_ID、TOKEN、SERVER_5G、SERVER_14G、PORT、CA_FILE。两个入口需到达同一后台，或由同一后台验证身份；本次未实现两个独立平台之间自动同步 Token。

173 由 `rtty-tenant-poc.service` 运行该脚本，保留 cron @reboot 原入口。手工/cron调用会委托同一服务；每个链路由脚本各启动一个客户端。重复执行入口不增加进程。Token 只存 0600 配置文件，不出现在进程参数。缺少共享配置或配置无效时不会退回匿名模式。

## 验证

既有 `cmd/tenant-e2e` 扩展真实 TLS 协议覆盖不同上报编号/同Token双链路、同链路冲突、明确链路选择、Token重置断开所有连接、旧Token拒绝、单路离线后新操作选择另一链路、软删除拒绝。

```sh
go build -o /tmp/rttys-multi ./cmd/rttys
go build -o /tmp/rttys-multi-check ./cmd/tenant-e2e
RTTY_BINARY=/tmp/rttys-multi RTTY_SOCKET_BINARY=/tmp/rttys-multi-check python3 scripts/run-local-e2e.py
```

2026-10-27 已部署公网 `47.111.133.127:5913`，设备TLS端口5912。173临时身份验证Token轮换后双链路一起掉线，仅修改一份配置的Token即可恢复；正式nvidia-173已启用双链路，两条链路的真实终端、文件上传下载均通过，文件SHA-256一致。临时平台身份和文件已清理。

本次173的两个逻辑连接都通过现有网络连接同一公网地址，证明鉴权、并发连接和路由选择；不等同于1.4G无线链路或两张网卡出口隔离验收。未重启整机。173原先的旧平台后台进程保留，未扩大本次切换范围。

公网备份：`/opt/ja-rttys/backups/shared-token-20261027-151028`；173成功迁移前备份：`/opt/rtty-start-backups/shared-20261027-151651`。回退需恢复旧服务unit和脚本、移除新增单文件配置，服务器回退时只会接受每设备一个连接；先回退客户端再回退服务器。
