# HTTP API v1

所有路径以 `/api/v1` 开头。私有项目设备使用请求头 `Authorization: Bearer <设备凭据>`，不把长期凭据放进 URL。

## 检查兼容更新

```http
GET /api/v1/update/controller/check?channel=stable&platform=test&arch=x64&hardware=board-a&sequence=1&security=0
Authorization: Bearer <设备凭据>
```

返回 `update`、兼容 `release`、`telemetry` 和 `server_time`。`release` 包含 `payload`（FH1 原始字节的 Base64）、`signature`（Ed25519 十六进制）、同源 `download` 路径等。

`update=false` 表示没有符合当前条件的较新版本，不代表所有其他平台没有新版本。`sequence` 是已安装发布序号，`security` 是客户端安全下限。筛选条件不是授权依据，凭据仍必须属于该项目。

不允许从服务器取得的时间直接替代可信时钟，也不能从响应中的普通 JSON 字段直接决定安装；先用预置公钥验证签名，再应用客户端兼容与防回滚策略。

## 历史与下载

- `GET /update/{slug}/latest`：与检查接口相同的兼容最新版本响应，可携带相同的平台/型号条件。
- `GET /update/{slug}/releases`：已发布记录，按发布序号倒序；`limit=1..200`（默认 200）、`before=发布序号` 分页。满页时返回 `X-Next-Before` 响应头，可作为下一页 before。
- `GET /artifacts/{id}`、`HEAD /artifacts/{id}`：授权下载、大小和 ETag。
- 支持单区间 Range / If-Range，206 为部分响应，416 为区间无效。
- ETag 为带双引号的文件 SHA-256；续传时要固定文件身份，ETag 变化禁止直接拼接。
- 公开项目的已发布元数据与文件无需凭据；设备状态永不公开。
- 单页最多 200 条，可持续向前分页；管理接口与公开发布页接口支持相同分页参数。

## 可选设备状态通道

为每台设备在后台创建独立凭据。设备凭据创建时只显示一次，服务器只保存摘要。

```http
POST /api/v1/update/controller/heartbeat
Authorization: Bearer <设备凭据>
Content-Type: application/json

{"version":"1.0.0","platform":"test","arch":"x64","hardware":"board-a","status":"idle"}
```

仅当以下条件**全部满足**时上报：

1. Hub 全局允许采集。
2. 项目创建/设置中启用设备状态。
3. 客户端本地显式同意。

检查响应 `telemetry.enabled` 不是客户端同意的替代品。C SDK 两个许可字段默认都为 0。建议遵守 `interval_seconds`，服务端至少间隔 5 秒。Hub 拒收时返回 `403 telemetry_disabled`；客户端应停止定时上报，不影响固件检测下载。

允许的升级状态：`idle`、`downloading`、`verified`、`installing`、`confirmed`、`failed`。只在实际安装并通过启动确认后上报 `confirmed`。

页面显示在线、心跳超时、未上报、采集关闭、凭据撤销。离线由超时推断，不执行端口探测。版本由客户端自报，不能证明固件真实运行状态。客户端关闭后不会额外发出“关闭声明”；Hub 在超时后显示心跳超时。

只保存用户命名、版本、平台/架构/型号、升级状态和最后心跳，不保存请求 IP、MAC、系统用户名或硬件序列号。关闭采集不自动删除旧记录，可在项目设备页清除。

关闭的是设备状态上报与台账采集，不代表网络匿名。普通更新检测仍可携带安装序号和适配参数，但不会据此更新设备台账。需要完全不发送安装版本的产品，可只拉取签名历史并在本地筛选，需自行处理前置版本与分页。

## 管理接口

网页使用 HttpOnly 会话 Cookie；写操作还要求 `X-CSRF-Token`。`GET /session` 返回当前会话和 CSRF 值。管理接口包括：

- `/projects`、`/projects/{id}`、`/projects/{id}/members`
- `/projects/{id}/releases`、`/releases/{id}/publish`
- `/projects/{id}/devices`、`DELETE /devices/{id}`
- `/projects/{id}/notifications`、`/projects/{id}/logs`
- `/settings`、`/users`

上传用 multipart，字段顺序为 `manifest`（签名 envelope JSON）、`notes`（可选）、`file`。最大文件 1 GiB，版本说明 32 KiB。先验签，再流式核对文件；上传不会自动发布。

错误统一为 `{"error":"machine_code","message":"可读原因"}`。常见状态为 400 参数/签名错误、401 凭据失效、403 权限/采集关闭、409 版本重复、429 限流、507 存储失败。设备必须处理错误，不能在错误后退化为跳过验证。

## 更新通知

事件包含 `event_id`、`event`、`project`、`version`、`sequence` 和相对 `check_path`。通知不是安装授权。

Webhook 的签名是 `HMAC-SHA256(secret, timestamp + "." + event_id + "." + raw_body)`，对应 `X-FH-Timestamp`、`X-FH-Event`、`X-FH-Signature`。接收方应校验时间窗口、签名并按事件编号去重。

MQTT 使用 QoS 1、不保留消息，按至少一次投递处理。所有渠道最多自动尝试 5 次。Telegram 当前仅发布提醒，不提供设备控制/远程命令。
