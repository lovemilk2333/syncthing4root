# syncthing4root

以 root 权限运行 Syncthing, 避免被系统强制停止

## 安装

1. 下载 Release 中的 zip 包
2. 在 Magisk / KernelSU / APatch 管理器中刷入模块
3. 根据提示选择下载源 (GitHub 直连或 ghfast.top 加速)
4. 重启设备

## 配置迁移 (从 Syncthing Android App)

若你之前使用 `com.nutomic.syncthingandroid`, 可将现有配置迁移至本模块 (其他应用可以使用类似操作, 请确保目标文件夹内目录结构类似于 `cert.pem  config.xml  https-cert.pem  https-key.pem  index-v0.14.0.db  key.pem`):

1. 安装模块后断开 Wifi 等连接, 以免重启后被局域网内其他 Syncthing 识别
2. 重启设备
3. 打开 Syncthing 点击右上角 小齿轮图标 > "关闭" 或运行 `su -c 'pkill syncthing'`
4. 复制配置文件 (可以使用文件管理器, 也可以使用如下脚本)
    ```sh
    # 1. 复制配置文件至 syncthing home 目录
    cp -r /data/data/com.nutomic.syncthingandroid/files/* /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/

    # 2. 修改目录及文件权限
    chmod 0755 /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home
    chmod 0644 /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/*
    ```

> [!WARNING]
> 如果你的 Syncthing 没有 Root 权限或者没有使用 Root 启动, 配置文件 `config.xml` 内的存储的文件夹路径极有可能是以 **内部存储** 为 `/` 的, 这会导致路径错乱
> 
> 为确保数据安全性, 请自行检查并迁移至实际路径, 例如 `/sync` -> `/storage/emulated/0/sync`  
> **本人及模块开发的一切相关人员不对未备份 Syncthing 配置和/或实际同步数据导致的数据丢失承担任何责任**

5. 迁移后重启设备或手动启动 Syncthing 即可  
    要手动启动 Syncthing, 可以运行
    ```sh
    su -c '/data/adb/service.d/syncthing_service.sh'
    ```
6. 恢复互联网连接

## 手动管理

- **打开 Web 界面**: 在模块管理器中点击 action 按钮, 自动跳转至 Syncthing Web UI
- **数据目录**: `/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/` (内含 `config.xml` 等)
- **自启动服务**: `/data/adb/service.d/syncthing_service.sh`

## 打包

```sh
sh build.sh
```

输出至 `release/<id>-<version>.zip`

## 许可

本模块配置与打包脚本使用 BSD 3-Clause 许可, 具体条款见 [LICENSE](LICENSE)  
syncthing 本身基于 MPL-2.0, 请参阅 https://github.com/syncthing/syncthing
