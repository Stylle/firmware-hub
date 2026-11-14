# Firmware Hub

账号菜单的「账号安全」支持修改登录密码（12–72 字节），已启用二次验证时还需当前验证码。更换验证器时先验证旧验证码、再扫描新二维码确认；确认前旧验证器继续有效，确认后旧密钥和本账户全部登录会话立即失效。待确认二维码有效期为 10 分钟。

轻量固件与软件分发中心。适用于嵌入式设备、Qt 上位机和其他可通过 HTTP(S) 接入的客户端。

## 最简部署

修改密码和数据目录后，在 NAS 的 Compose 项目或普通 Docker 中部署：

```yaml
services:
  firmware-hub:
    image: stylle/firmware-hub:latest
    container_name: firmware-hub
    restart: unless-stopped
    environment:
      - USER=admin # 首次必填：管理员用户名
      - PASSWORD=请替换为至少12字节的强密码 # 首次必填：12–72 字节
    ports:
      - "28183:8080" # 左侧可修改；公网请使用 HTTPS 反向代理
    volumes:
      - ./data:/data # 必须持久化；左侧可换为 NAS 独立目录
```

打开 `http://服务器地址:28183`。不需要 `.env`、Setup Token、特权模式或 Docker Socket。

首次创建账号后，`USER/PASSWORD` 不再覆盖已有账户，可以移除 `PASSWORD`。重置时在 Compose 填入新值，然后执行：

```sh
docker compose run --rm firmware-hub admin-sync
```

该命令重置指定管理员、移除该账号的 TOTP 绑定，并撤销所有网页会话。环境变量只用于初始化/此维护命令，不是持续生效的密码配置。

## 已实现

- 多用户、可选 TOTP 验证器登录、独立项目、维护/审核/查看权限。
- 默认账号发布与署名；可选项目发布密码。高级 Ed25519 签名不强制。
- 软件整包、单 DLL/EXE 按文件更新；版本、通道、平台、架构、型号筛选。
- 草稿、发布、撤回、历史列表、兼容更新检测。
- 流式上传下载、SHA-256、HTTP Range / ETag。
- 可选设备当前版本、最近心跳及升级状态；客户端和 Hub 可独立关闭。
- MQTT 更新提醒、HMAC Webhook、Telegram 发布通知、重试记录。
- 安全 Markdown 发布说明、可选公开发布页。
- C99 / C++17 SDK、Qt 5.14+ / Qt 6 参考客户端。
- 单容器、SQLite、本地存储、NAS 独立目录初始化、离线备份命令。
- 无需登录的公开版本中心、账号登录历史、会话撤销、项目操作记录。

## 开始发布

1. 登录后创建项目，填写名称与标识。公钥留空即可，不需要 `fwtool`。
2. 上传文件、填写版本和说明，保存草稿，然后发布。发布者由服务器记录为当前账号，不能手填冒名。
3. 项目可开启“公开发布页”；访客从登录页的“浏览公开版本”进入 `/?public`，查看历史并下载。私有项目不显示。

Qt 软件：将 `windeployqt` 生成目录内的文件压缩为 ZIP，选择完整文件/软件整包；只改 DLL/EXE 时选择按文件更新，填写适用旧版本和相对目标路径。客户端需接入更新器，不是所有现有软件自动具备升级功能。详见 [桌面软件更新](https://github.com/Stylle/firmware-hub/blob/main/docs/desktop-updates.md)。

### 高级：独立数字签名（可选）

仅希望客户端独立验证发布者时使用：

```sh
go build -o fwtool ./cmd/fwtool
fwtool keygen --out publisher.key
```

在网页创建项目，只填写输出的**公钥**。再签名文件：

```sh
fwtool sign --key publisher.key --file firmware.bin --project controller \
  --version 1.0.0 --sequence 1 --security 0 \
  --platform any --arch any --hardware board-a --out manifest.json
```

上传 `manifest.json` 和对应文件，填写版本说明，审核后发布。版本号在签名前确定；网页不重签、不接收私钥。

## 文档

- [部署、备份与更新](https://github.com/Stylle/firmware-hub/blob/main/docs/deployment.md)
- [接口与设备状态](https://github.com/Stylle/firmware-hub/blob/main/docs/api.md)
- [SDK 与 Qt 示例](https://github.com/Stylle/firmware-hub/blob/main/docs/sdk.md)
- [整包 / DLL / EXE 更新](https://github.com/Stylle/firmware-hub/blob/main/docs/desktop-updates.md)
- [RS485 与最小设备示例](https://github.com/Stylle/firmware-hub/blob/main/docs/rs485.md)
- [安全边界和 FH1 格式](https://github.com/Stylle/firmware-hub/blob/main/docs/security.md)
- [开发计划与兼容范围](https://github.com/Stylle/firmware-hub/blob/main/docs/roadmap.md)

## 安全边界

这是 **0.x 首版**。FH1 是明确版本化的签名清单格式，**不宣称完整 TUF、SUIT 或 LwM2M 兼容**。完整信任根轮换、多签和特殊协议适配见开发计划。

默认账号模式依靠网站权限、发布密码、HTTPS 和日志，SHA-256 只检查传输完整性；**服务器被攻破后，仍可能分发恶意更新**。账号署名不是数字签名，也不是不可篡改的外部审计证明。

高级签名模式的私钥独立保管，客户端预置公钥并严格验签。状态由客户端自报，不是硬件可信证明；下载完成不等于安装成功。首次写入新设备 Bootloader 与后续在线升级是两件事。

SDK 不执行远程命令，不自动刷写任意硬件。真实安装应接入设备自身的安全启动、分区与启动确认机制。


## 界面、关于与兼容升级

- 保持原页面布局，仅统一配色与组件样式；亮色／暗色／跟随系统，默认跟随系统，支持中英切换。
- 菜单中的「关于」或 `/about.html` 关于页面 查看版本、版权、许可与数据安全边界。
- 本次 UI 不修改业务接口、数据库和附件布局。升级前备份完整 data，重建时继续使用同一挂载；不要删除数据卷。
- 容器服务使用 UID/GID 10001；入口仅准备专用 data 目录。NAS ACL、只读目录或特殊挂载不能自动绕过，详见迁移说明。
- [升级与迁移](https://github.com/Stylle/firmware-hub/blob/main/docs/UI-UPGRADE.md) · [后续 Agent 复用手册](https://github.com/Stylle/firmware-hub/blob/main/docs/AGENT-HANDBOOK.md)。手册登记发布所需的 Secret 名称与权限，绝不包含真实 Token。

## 开源与部署说明

本项目采用 [MIT License](https://github.com/Stylle/firmware-hub/blob/main/LICENSE)，可自行部署与按许可证使用。源码和镜像不包含维护者的真实部署地址、个人邮箱、访问令牌、开发电脑路径或业务数据。项目名称、公开源码链接和第三方许可证署名保留。

上面的最简 Compose 即可启动。只有首次账号初始化、端口与持久化是基础部署所需；Telegram、Webhook、MQTT 或远程存储等集成均按需配置，详见使用文档。升级时保留完整数据目录。


## 网页整站备份与异地恢复

在「系统管理 → 系统备份与恢复」下载完整 ZIP 备份包，包含账号、TOTP、项目、设备凭据、版本记录、固件文件、通知配置及 master.key。导出使用 SQLite 一致性快照，排除浏览器登录会话。包内包含解密配置所需的密钥，ZIP 本身不加密，请存放在受保护的位置。

在另一台机器部署同一项目的相同或兼容新版本，先用新部署账号登录；选择备份包，核对文件名，勾选替换确认后导入。系统先检查包类型、路径、文件校验和、数据库及密钥，再保留原数据至 `/data/previous-*`，自动重新加载服务。随后使用备份中的原账号/验证器登录；不需要手动重启或复制密钥。无效备份不会替换当前数据。单包解压内容上限 20 GB，需为上传包、展开包及原数据预留空间。

迁移的是应用数据，不含 Docker 端口、挂载目录、反向代理、TLS 证书及应用外部文件。新环境仍需能访问保存的外部服务地址，并具备对应网络权限；环境变量 USER/PASSWORD 不会覆盖恢复的账号。不要把原实例和恢复实例同时连接同一个 Telegram 机器人。恢复前请另行导出当前数据；确认恢复正常后再自行清理回退目录。

旧命令行 `backup` 生成的 ZIP 可按原来的停服解压流程恢复；网页导入使用带 MANIFEST.json 的新格式，勿混用。离线发布签名私钥不在服务端，仍需自行备份。

## 维护与验证

格式、共享组件、反向代理与平台验收见 [维护约定](https://github.com/Stylle/firmware-hub/blob/main/docs/MAINTENANCE.md)。升级前备份完整数据目录及应用密钥，保留原数据卷。
