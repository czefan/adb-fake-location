# ADB 虚拟定位小工具

用电脑网页选点或输入坐标，通过 ADB 持续模拟 Android GPS 位置。Go 后端、原生网页和 Leaflet，手机无需安装额外 APK。

已支持地图选点、坐标转换、持续更新、停止恢复及只读诊断。**模拟位置能否被采用取决于目标应用**：当前手机上的高德、腾讯地图存在兼容性限制，详见[实测记录](docs/compatibility.md)。

## 开发运行与构建

需要 Go（版本见 `go.mod`）和 Android Platform Tools。使用编译后的程序只需 ADB，无需 Go 或 Node.js。

```powershell
# 从源码运行
go run ./cmd/adb-fake-location

# 只读检查：不会开始模拟或触发恢复
go run ./cmd/adb-fake-location --check

# 编译后运行
.\scripts\build.ps1
.\dist\adb-fake-location.exe
```

构建脚本统一输出到 `dist/`。源码仓库不保留旧 EXE，也不需要 GoGoGo 参考工程。

启动成功后打开 `http://127.0.0.1:8080`。手机需开启调试、授权电脑，并开启系统定位；无线调试先通过 ADB 配对连接。初始验证范围为 Android 12+，最终以设备实际命令能力为准。

```powershell
# 多设备时指定目标；无线设备使用实际的连接端口
go run ./cmd/adb-fake-location --serial DEVICE_SERIAL

# 自定义 ADB 路径、服务端口或地图配置
go run ./cmd/adb-fake-location --adb-path "D:\platform-tools\adb.exe" --port 9000 --no-browser
go run ./cmd/adb-fake-location --config ./config.example.json
```

## 使用

1. 等待显示“设备就绪”，地图选点或输入坐标。
2. 点击“开始模拟”；运行中可用“应用新坐标”更新目标。
3. 在实际使用的手机应用中确认效果。
4. 完成后点击“停止模拟”，等待清理成功；真实定位可能需要稍后刷新。

地图点击只是候选位置，不会立即发送。绿色标记显示最近成功发送的位置。输入坐标系与底图坐标系分开处理，避免回显偏移。

关闭网页不会停止模拟。正常退出会尝试清理；断连或异常退出时保留恢复记录，重新连接原设备后点击“执行清理恢复”。不要直接删除未完成的恢复记录。

## 地图配置

默认使用现有的高德瓦片与 Leaflet；当前加载、缩放和选点已验证可用，不代表承诺该瓦片接口长期开放或永久免费。地图有问题时，坐标输入仍能使用。

配置文件默认位于 `%LOCALAPPDATA%\adb-fake-location\config.json`，也可通过 `--config` 指定。参考根目录的 `config.example.json`，修改后重启。

- `tile_url`：HTTPS XYZ 模板，含 `{x}`、`{y}`、`{z}`，可选 `{s}`。
- `coordinate_system`：明确填写 `WGS-84` 或 `GCJ-02`，不会根据地址猜测。
- `attribution`：来源署名；`min_zoom` / `max_zoom`：缩放范围。
- `subdomains`：子域列表，例如 `1234`。

只使用坐标输入时：

```json
{"tile_url": "", "coordinate_system": "WGS-84"}
```

指定的配置文件不存在或内容错误时，会显示原因并进入坐标输入模式。未指定配置、且用户目录没有配置文件时，才使用默认地图。

## 项目结构

```text
cmd/adb-fake-location/  程序入口
internal/app/          启动、HTTP 接口、会话、恢复记录与配置
internal/adb/          ADB 命令、解析与兼容处理
internal/geo/          坐标校验与转换
internal/platform/     本地路径、进程窗口与单实例锁
web/                   网页资源及 Go embed 入口
scripts/               构建、只读检查和测试脚本
testdata/fakeadb/       自动化测试使用的模拟 ADB
docs/                  架构说明与兼容性记录
```

测试放在对应 Go 包内。具体职责与依赖关系见[架构说明](docs/architecture.md)。

## 状态文件与旧版记录

Windows 用户数据保存在 `%LOCALAPPDATA%\adb-fake-location\`；Linux/macOS 使用 Go 的用户配置目录。包含可选 `config.json`、会话恢复文件 `session.json` 和操作系统管理的 `instance.lock`。

记录设备标识和原 AppOps 状态后才修改设备；清理成功后删除会话记录。只清理记录所属设备，恢复由本工具改变的权限，不重置其他应用的设置。

旧 demo 的记录若缺少原授权状态，无法自动猜测恢复。先退出旧工具，核对记录中的设备：

```powershell
adb -s SERIAL shell dumpsys location
adb -s SERIAL shell cmd appops get com.android.shell android:mock_location
```

确认遗留 GPS 测试提供商属于旧 demo 时可执行 `adb -s SERIAL shell cmd location providers remove-test-provider gps`。旧包级授权应按已知原值还原，不要全局重置 AppOps。确认设备状态处理完成后，将旧记录改名留存，再启动新会话。

## 验证

```powershell
go test ./...
go test -race ./...
go vet ./...
node --check web/static/app.js
```

竞态检查需要 Go 支持的 C 工具链；Node 只用于可选语法检查。自动化测试使用模拟 ADB，不修改真机；通过测试不等于所有手机应用都兼容。

PowerShell 7+ 的辅助脚本支持只读检查：

```powershell
pwsh -File scripts/verify-adb.ps1 -Action check -Serial SERIAL
```

需要进行临时定位测试时，先启动工具，再运行脚本。它复用工具的会话管理与恢复流程：

```powershell
pwsh -File scripts/verify-adb.ps1 -Action test -Serial SERIAL -Lat 39.9 -Lng 116.4 -DurationSec 10
pwsh -File scripts/verify-adb.ps1 -Action clean -BaseUrl http://127.0.0.1:8080
```

部分系统的定位帮助命令会返回 255，工具只对完整有效的只读帮助响应兼容此行为；修改命令仍严格检查错误。

Leaflet 的许可证保存在 `web/static/vendor/leaflet/LICENSE`。
