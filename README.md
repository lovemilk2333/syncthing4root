# syncthing4root

以 root 权限运行 Syncthing, 避免被系统强制停止. 自带 **Web 管理界面** 和 **Web API** (TLS + Basic Auth 鉴权).

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
| 🔄 Check & Update Syncthing | 检查并更新 Syncthing 到最新版 |

管理后台默认地址: `https://127.0.0.1:48344/ui/` (默认账号 `admin` / `admin`)

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
| POST | `/api/start` | 启动 Syncthing |
| POST | `/api/stop` | 停止 Syncthing |
| GET | `/api/syncthing-url` | Syncthing GUI 地址 |
| POST | `/api/open-syncthing` | 在浏览器中打开 Syncthing UI |
| GET | `/api/autostart` | 自启动开关状态 |
| POST | `/api/autostart/enable` | 启用开机自启动 |
| POST | `/api/autostart/disable` | 禁用开机自启动 |
| POST | `/api/update` | 更新 Syncthing 到最新版 |
| POST | `/api/change-password` | 修改登录密码 (需旧密码验证) |
| POST | `/api/change-username` | 修改登录用户名 (需密码验证) |

### Web 服务器参数

```
syncthing4root_webserver --port <端口> --module-dir <模块目录> [--no-tls]
```

- `--port`: 监听端口 (默认 `48344`)
- `--module-dir`: 模块路径 (默认根据自身路径推算)
- `--no-tls`: 禁用 TLS (使用 HTTP 明文)

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

5. 迁移后重启设备或手动启动 Syncthing:
    ```sh
    su -c '/data/adb/service.d/syncthing_service.sh'
    ```
6. 恢复互联网连接

## 安全性

- **TLS**: 首次启动时自动生成 4096 位 RSA 自签名证书 (有效期 10 年), 使用 HTTPS 加密通信
- **鉴权**: HTTP Basic Auth, 默认用户名 `admin`, 默认密码 `admin` (首次运行写入 `.auth_config`)
- **密码存储**: `.auth_config` 中的密码以 bcrypt 哈希存储 (与 `caddy hash-password` 同算法), 不保存明文
- **自启动开关**: 禁用自启动本质是在数据目录创建 `.autostart_disabled` 标志文件, 开机服务脚本检测到该文件后跳过启动

> [!WARNING]
> **版本回退提示**: 自本版本起, `.auth_config` 的密码字段由明文改为 bcrypt 哈希. 升级后旧的明文密码会在首次启动时被自动改写为哈希, 此过程**不可逆**.
> 若之后回退到旧版本 (旧版按明文逐字比较密码), 将无法用原密码登录. 回退时请手动把 `.auth_config` 的 `password=` 改回明文, 或删除该文件让模块重新生成默认 `admin` / `admin`.

## 打包

```sh
sh build.sh
```

构建流程:
1. 交叉编译 Go Web 服务器 -> `build/syncthing4root_webserver` (ARM64)
2. 将模块文件复制到 `build/` 目录
3. 从 `build/` 打包 zip -> `release/<id>-<version>.zip`

### 开发环境

```sh
# Go 交叉编译工具链 (Go 1.26+)
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
