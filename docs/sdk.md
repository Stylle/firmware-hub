# C / C++ SDK 与 Qt 接入

## 文件和注释

- `sdk/c/fw_client.h/.c`：C99，无网络/堆分配/Flash 依赖。
- `sdk/cpp/fw_client.hpp/.cpp`：C++17 轻封装，校验返回错误码；标准容器分配仍可能抛出 bad_alloc。严格无堆/无异常的集成使用 C 接口。
- `examples/qt/hub_client.hpp/.cpp`：Qt 异步 HTTP(S) 适配与心跳。
- `examples/qt/download_task.hpp`：受控流式下载、最多 3 次同进程 Range 续传。
- `sdk/third_party`：原样保留的 Monocypher Ed25519 验签后端与许可证。

公共接口采用 Doxygen 注释，说明参数、所有权、返回值、默认行为与安全前置条件。SDK 不持有私钥，不自动重启或执行安装脚本。

## MCU 接入顺序

1. 预置项目、型号及可信公钥；准备可信时钟、持久化安全状态。
2. 实现 `fw_http_fn`：TLS、域名验证、限时、响应上限、鉴权和重定向策略。
3. 调用 `fw_check`，用平台 JSON 库提取并严格解码 `release.payload/signature`。
4. 调用 `fw_verify_manifest`。失败立即停止，不自动降级到不验签。
5. 按签名的大小限制流式下载，逐块计算 SHA-256，不将大文件完整放进内存。
6. 调用 `fw_verify_artifact`。通过后才能交给型号专用安装适配器。
7. 按 Bootloader 的规则安装、启动自检、确认或恢复；适时持久化版本与安全计数。

SDK 故意不内置一套通用 JSON 库、TLS 栈或 Flash 驱动。Qt 适配使用 Qt JSON/Network；其他平台可使用自己的成熟库。只复制头文件而不实现上述校验步骤不构成安全 OTA。

`fw_policy` 中所有字符串指针必须在调用期间有效；C++ Client 复制的是策略及指针，不取得指针所有权。输出 manifest 为调用者自己的结构体。所有非零结果均不能授权安装。

## 可选心跳

```c
fw_policy policy = {0};
/* 填写 project/channel/platform/arch/hardware 等必需字段。 */
policy.telemetry_enabled = 0;     /* 客户端默认关闭 */
policy.hub_telemetry_enabled = 0; /* 来自最近检查结果；不能替代本地同意 */
```

两个字段都为 1 才会调用传输函数，否则 `fw_heartbeat` 返回 `FW_DISABLED`，不产生上报请求。收到 Hub 的关闭响应后，适配器应停止心跳定时器。

设备凭据应独立分配并保护，不能将一个有发布权限的账号用于设备。不要在日志打印 Token。

## Qt 编译

安装 Qt Widgets + Network 以及匹配编译器，使用影子构建：

```sh
mkdir -p build/qt
cd build/qt
qmake ../../examples/qt/hub-demo.pro
make
```

Windows MinGW 使用 `mingw32-make`。工程设置 `object_parallel_to_source`，避免 C 与 C++ 同名源文件产生对象文件冲突。支持 Qt 5.14+ / Qt 6 的相应环境；Qt 5.14 属于旧工具链，生产升级代理建议使用受维护的 Qt/SSL 版本。

界面配置服务地址、项目、公钥、设备凭据、当前版本与型号。设备凭据不持久化；非敏感配置及防回放状态使用 QSettings 保存。测试可用 `FH_STATE_DIR` 指定独立本地状态目录。

Qt 下载先验证签名和兼容性，再写入 QSaveFile，完整摘要校验通过才原子提交。取消、TLS 失败或摘要不符不会替换原目标文件。网络中断时固定已验证文件的 ETag 和已收字节续传；进程退出后不保留可恢复下载任务。

Qt 示例不跟随下载重定向，不把 Token 发往其他源。HTTP 仅允许回环测试；HTTPS 校验证书与主机名。私有 CA 可在集成时设置 `Config.trustedCAFile`；测试模式支持 `FH_CA_FILE`，不信任从 Hub 下载的 CA，不调用 `ignoreSslErrors()`。

下载成功不改变“当前已安装版本”。示例不实际刷机，也不会伪造启动确认。真实设备的固件切换和确认须由安装适配器实现。

## 自动验证

```sh
go test ./...
sh tests/sdk.sh
```

Windows 本机 Docker 演示可运行 `scripts/seed-demo.mjs` 创建隔离示例，随后编译 Qt，运行 `scripts/test-qt.mjs`。测试使用 `.local` 内专用连接数据，不打印凭据。

`tests/qt-integration.mjs` 使用临时本机故障代理，验证真实 Qt 下载、Range 重连、错误签名/ETag/摘要拒绝、三方采集开关及可信/不可信 HTTPS。先运行 `go run ./cmd/testcert` 生成临时测试证书，并准备匹配的 Qt SSL 运行库。

这不是实际开发板的断电/刷写测试；硬件验证按适配型号单独登记。
