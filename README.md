# syncthing4root

以 root 权限运行 Syncthing, 避免被系统强制停止. 自带 **Web 管理界面** 和 **Web API** (TLS + Basic Auth 鉴权).

Run Syncthing as root so the system cannot force-stop it. Comes with a **web management UI** and a **web API** (TLS + Basic Auth).

中文 | [English](README.en.md)

## 安装

1. 下载 Release 中的 zip 包
2. 在 Magisk / KernelSU / APatch 管理器中刷入模块
3. 根据提示选择下载源 (GitHub 直连或 ghfast.top 加速)
4. 重启设备

## 使用方法

### 管理界面 (Web UI)

点击模块管理器的 **Action 按钮 (>)**, 自动启动管理后台并在浏览器中打开. 界面支持:

| 功能 | 说明 |
|------|------|
| 📂 Open Syncthing UI | 打开 Syncthing 原生 Web 管理页面 |
| ▶ Start | 启动 Syncthing 守护进程 |
| ■ Stop | 停止 Syncthing |
| Auto-start on boot | 开关开机自启动 |
| 🔑 Account | 修改管理后台登录用户名 / 密码 |
| 📜 Logs | 可切换 Syncthing 输出 / Web 服务 / 状态切换日志; 全部只保存在内存环形缓冲区, 不落盘; 状态切换记录 ISO 时间与原因; 保留条目数可配置 (默认 128) |
| 🔄 Check & Update Syncthing | 检查并更新 Syncthing 到最新版 |
| 🌐 DNS Resolver | 配置更新检查使用的 DNS (自动检测局域网 DNS / 指定 IP / DoH / DoT), 自动模式忽略非局域网 DNS; 系统 DNS 通过 `github.com/AdguardTeam/golibs/netutil/sysresolv` 发现, 再回退到 Android 专有探针, 解决 Android root 进程 DNS 解析失败问题 |

管理后台默认地址: `https://127.0.0.1:48344/ui/` (默认账号 `admin` / `admin`)

> 管理界面基于 **Vue 3 + PrimeVue 4 (MIT)**, 通过 **SSE (Server-Sent Events)** 实时推送状态, 页面本身不做任何轮询; 网络 / 省电状态变化由 **Android 系统事件** (`logcat -b events` / netlink / inotify) 驱动, 不再定期请求电池 / WiFi 状态.
>
> 使用 PrimeVue 4 而非 PrimeVue 5: v5 自带 license 校验 (`#p-license-host` 悬浮提示 / 联网校验), 不适合离线 root 模块; v4 为 MIT 开源版本, 无联网校验.

> 自签名证书会导致浏览器显示安全警告, 点击"高级 / 继续前往"即可.
> 若浏览器不支持自签名证书, 可使用 `--no-tls` 参数降级为 HTTP:
>
> ```sh
> su -c '/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing4root_webserver --port 48344 --module-dir /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root --no-tls'
> ```

### 手动管理

- **数据目录**: `/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/` (内含 `config.xml` 等)
- **自启动服务**: `/data/adb/service.d/syncthing_service.sh`
- **鉴权配置**: `/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/.auth_config` (格式: `username=...` / `password=<bcrypt 哈希>`)
- **TLS 证书**: `/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/tls.{crt,key}`

### API 接口

所有 `/api/*` 端点使用 HTTP Basic Auth, 默认用户名 `admin`, 默认密码 `admin` (首次运行写入 `.auth_config`)

| 方法 | 端点 | 功能 |
|------|------|------|
| GET | `/api/status` | Syncthing 运行状态, PID, GUI 地址 |
| GET | `/api/events` | SSE 实时事件流 (状态 / 网络策略快照推送) |
| POST | `/api/start` | 启动 Syncthing |
| POST | `/api/stop` | 停止 Syncthing |
| GET | `/api/syncthing-url` | Syncthing GUI 地址 |
| POST | `/api/open-syncthing` | 在浏览器中打开 Syncthing UI |
| GET | `/api/autostart` | 自启动开关状态 |
| POST | `/api/autostart/enable` | 启用开机自启动 |
| POST | `/api/autostart/disable` | 禁用开机自启动 |
| POST | `/api/update` | 更新 Syncthing 到最新版 |
| GET  | `/api/dns-config` | 读取 DNS 解析器配置 + 自动检测的 DNS 列表 (每项标注是否为局域网) |
| POST | `/api/dns-config` | 保存 DNS 配置 (留空=自动 / IP / IP:端口 / DoH `https://` / DoT `tls://`) |
| POST | `/api/account` | 一次修改用户名和/或密码 (需当前密码验证, 字段留空表示不修改) |
| POST | `/api/change-password` | 修改登录密码 (需旧密码验证) |
| POST | `/api/change-username` | 修改登录用户名 (需密码验证) |
| GET  | `/api/netpolicy` | 读取网络策略配置 + 当前网络状态 |
| POST | `/api/netpolicy` | 保存网络策略配置 (热更新, 立即重新评估) |
| GET  | `/api/netpolicy/scan` | 扫描当前传输类型 / WiFi SSID (供白名单填充) |
| GET  | `/api/logs` | 列出内存日志源 (`state` / `syncthing` / `webserver`) 及当前保留条目数 |
| GET  | `/api/logs/stream?source=state` | SSE 实时日志流 (先发送内存中保留的历史条目) |
| POST | `/api/logs/config` | 设置内存保留条目数 `{"lines":128}` (16–4000), 立即丢弃多余条目 |

### Web 服务器参数

```
syncthing4root_webserver --port <端口> --module-dir <模块目录> [--no-tls]
```

- `--port`: 监听端口 (默认 `48344`)
- `--module-dir`: 模块路径 (默认根据自身路径推算)
- `--no-tls`: 禁用 TLS (使用 HTTP 明文)

### 网络策略 (按网络状态自动启停)

管理界面的 **📶 Syncthing Running Policy** 卡片可根据当前网络状态自动启动 / 停止 Syncthing. 默认关闭, 需手动开启.

- **总开关**: 启用后由策略接管启停; 关闭时为手动模式 (状态栏显示 `auto` / `manual` 徽标).
- **事件驱动检测**: 不再按固定周期轮询系统状态. 直接消费 **Android 事件**: `logcat -b events` 中的 WiFi / 网络 / 电池 / 省电事件, 配合 netlink (`ip monitor`) 与 inotify (Android settings 数据库), 状态变化立即重新评估并推送到前端.
- **探测间隔**: 仅用于 `probe` (可达性探测) —— 可达性检测本质上是周期性的; 未启用 `probe` 时策略完全由事件驱动. 默认 30 秒, 范围 5–3600.

**运行条件 (布尔表达式)**: 用一个表达式描述"何时应运行 Syncthing", 支持 `AND` / `OR` / `NOT` 与括号任意嵌套.

四个可用条件项 (term), 为 `true` 的含义:

| term | true 时 | 配置面板 |
|------|---------|---------|
| `wifi` | 连接到白名单内 SSID 的 WiFi | WiFi 白名单 (支持"📡 扫描当前 SSID") |
| `cellular` | 当前在蜂窝 (移动数据) 网络 | — |
| `power` | 系统省电模式 (Battery Saver) 开启 | — |
| `probe` | 探测目标可达 | Ping/TCPing + target + timeout + 上/下线阈值 (滞回) |

示例:

```
wifi AND NOT cellular              # 在白名单 WiFi 且不在蜂窝时运行
wifi AND (probe OR NOT power)      # 白名单 WiFi, 且 (探测通 或 非省电)
NOT cellular AND NOT power         # 只要不在蜂窝且非省电就运行
```

- 语法: 关键字大小写不敏感; 也接受 `&&` / `||` / `!` 作为 `AND` / `OR` / `NOT` 的同义符.
- **空表达式 = 恒运行** (无约束).
- 某个 term 对应的下方面板**关闭**时: `wifi` / `cellular` / `probe` 恒为 `true`, 而 `power` 恒为 `false` (使 `NOT power` 在 `AND` 下保持中性). 注意在 `NOT`/`OR` 下这些默认值会影响结果.
- 保存时后端会校验表达式语法与标识符, 非法则拒绝保存并提示错误.
- 探测阈值 `up`/`down`: 连续成功/失败次数达到阈值才切换状态 (滞回, 避免网络抖动导致频繁启停).

> [!NOTE]
> - 策略开启时会覆盖手动 Start / Stop 操作 (下一次策略重新评估时按策略调和), 状态栏 `auto` 徽标即表示处于自动模式.
> - 网络 / 省电检测完全由事件驱动: Android 事件日志 (`logcat -b events`, WiFi / 网络 / 电池 / 省电广播的底层事件) + netlink (`ip monitor`) + inotify (`settings_global.xml`). 仅在事件到达时执行一次 `ip route` / `cmd wifi status` / `settings get global low_power`, 不再定期请求电池 / WiFi 状态; 无需 Frida/Xposed hook.
> - 管理界面通过 SSE (`/api/events`) 实时接收状态, 页面本身不轮询. 状态卡片会显示当前可用的 Android 事件源 (`logcat` / `netlink` / `inotify`).
> - `probe` (可达性) 是唯一的周期性检查 —— 网络可达性无法通过系统事件获知. 未启用 `probe` 时不存在任何周期检测.
> - 若 Android 系统命令不可用或 SSID 识别失败时 `wifi` 判定为 `false`.
> - 配置持久化于 `syncthing/.netpolicy.json`; 旧版本 (beta3/4) 的 `combine` 配置会在首次加载时自动迁移为等价表达式.

## 配置迁移 (从 Syncthing Android App)

若你之前使用 `com.nutomic.syncthingandroid`, 可将现有配置迁移至本模块:

1. 安装模块后断开 Wifi 等连接, 以免重启后被局域网内其他 Syncthing 识别
2. 重启设备
3. 打开 Syncthing 点击右上角小齿轮图标 >"关闭"或运行 `su -c 'pkill syncthing'`
4. 复制配置文件:
    ```sh
    # 复制配置文件至 syncthing home 目录
    cp -r /data/data/com.nutomic.syncthingandroid/files/* /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/

    # 修改目录及文件权限
    chmod 0755 /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home
    chmod 0644 /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/*
    ```

> [!WARNING]
> 如果你的 Syncthing 没有 Root 权限或没有使用 Root 启动, 配置文件 `config.xml` 内的存储文件夹路径极有可能是以**内部存储**为 `/` 的, 这会导致路径错乱.
>
> 为确保数据安全性, 请自行检查并迁移至实际路径, 例如 `/sync` -> `/storage/emulated/0/sync`.
> **本人及模块开发的一切相关人员不对未备份 Syncthing 配置和/或实际同步数据导致的数据丢失承担任何责任.**

5. 迁移后重启设备, 或在管理界面点击 **Start**
   (开机时由管理后台根据自启动开关启动 Syncthing, 其输出保存在内存日志中)
6. 恢复互联网连接

## 安全性

- **TLS**: 首次启动时自动生成 4096 位 RSA 自签名证书 (有效期 10 年), 使用 HTTPS 加密通信
- **鉴权**: HTTP Basic Auth, 默认用户名 `admin`, 默认密码 `admin` (首次运行写入 `.auth_config`)
- **密码存储**: `.auth_config` 中的密码以 bcrypt 哈希存储 (与 `caddy hash-password` 同算法), 不保存明文
- **自启动开关**: 禁用自启动本质是在数据目录创建 `.autostart_disabled` 标志文件, 管理后台启动时检测到该文件后跳过拉起 Syncthing
- **日志**: Syncthing 输出、Web 后台输出和状态切换记录都只写入内存环形缓冲区 (默认各保留 128 条), 不创建日志文件; 状态切换条目使用 ISO-8601 时间戳 (含 `+08:00` 这类数字时区偏移) 并记录切换原因

> [!WARNING]
> **版本回退提示**: 自本版本起, `.auth_config` 的密码字段由明文改为 bcrypt 哈希. 升级后旧的明文密码会在首次启动时被自动改写为哈希, 此过程**不可逆**.
> 若之后回退到旧版本 (旧版按明文逐字比较密码), 将无法用原密码登录. 回退时请手动把 `.auth_config` 的 `password=` 改回明文, 或删除该文件让模块重新生成默认 `admin` / `admin`.

## 打包

```sh
sh build.sh
```

构建流程:
1. 构建前端 `web/frontend` (Vue 3 + PrimeVue + Vite) -> `web/frontend/dist`
2. 交叉编译 Go Web 服务器 -> `build/syncthing4root_webserver` (ARM64, 前端通过 `go:embed` 打包进二进制)
3. 将模块文件复制到 `build/` 目录
4. 从 `build/` 打包 zip -> `release/<id>-<version>.zip`

### 开发环境

```sh
# 前端 (Vue 3 + PrimeVue)
node -v            # Node.js 20+
cd web/frontend
npm install
npm run dev        # 本地开发 (API 需自行反代到后端)
npm run build      # 产出 dist/, 由 Go embed 打包

# Go 交叉编译工具链 (Go 1.27+)
# 默认已支持 ARM64 交叉编译
go version

# 编译 (build.sh 会自动执行)
cd web && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ../build/syncthing4root_webserver .
```

### `versionCode` 规则
`versionCode` 由 8 位日期, 1 位 alpha/beta 标识 与 3 位版本流水号组成, 对于一天内发布的多个版本, 流水号依次递增 1, 例如 `202607070001` 代表 2026 年 07 月 07 日的第一个稳定版本

对于 alpha/beta 版本, 其由下一个正式版的日期进行 $-1$, alpha/beta 标识置 1 和流水号组成, 例如 `202607151001` 代表 2026 年 07 月 16 日的第一个 alpha/beta 版本, `202607151002` 代表 2026 年 07 月 16 日的第二个 alpha/beta 版本, `202607160001` 代表 2026 年 07 月 16 日的第一个稳定版本

## 许可

本模块配置与打包脚本使用 BSD 3-Clause 许可, 具体条款见 [LICENSE](LICENSE).
Syncthing 本身基于 MPL-2.0, 请参阅 https://github.com/syncthing/syncthing.
