# DJI Mic 接收器控制台

Windows 上读取 **DJI Mic Mini 系列接收器**（DMMR02 / Mobile RX，USB `0x2ca3:0x4011`）的实时状态并写入它的设置。
界面用 Go 与 [mygo](https://github.com/egoist/mygo) 的原生 UI 工具包绘制，不使用网页视图；USB 访问通过 WinUSB 直接完成。

非官方工具，与 DJI 没有关系。

## 它做什么

- **状态**：接收器与两支发射器的连接、电量、充电、实时输入电平、增益旋钮位置、序列号与固件版本。接收器以约 10 次/秒主动推送，程序只被动接收。
- **设置**：降噪强度与开关、按键切换降噪、低切、声道模式、安全音轨、自动限幅、接收器/发射器自动关机、跟随相机开关机、发射器指示灯、免拔插外放、每支发射器的音色。
- **驱动**：在接收器的**厂商接口**上安装 Microsoft 自带的 WinUSB 驱动，这是 Windows 上读写这个接口的前提。接收器的音频接口（系统录音设备）与按键接口保持系统驱动，录音不受影响。

## 运行

需要 Windows 10/11 与 Go 1.27.1 或更高（mygo 要求的工具链会由 Go 自动下载）。

```bat
go run .                      :: 启动图形界面
go run ./cmd/djiprobe         :: 只扫描设备并读取状态，把协议层的结果打到终端
go run ./cmd/djiprobe -scan   :: 只报告设备树与驱动绑定情况
go test ./...                 :: 协议、解码与界面几何的测试
go run ./cmd/uirender         :: 把每个页面渲染成 PNG，用于查看布局
```

打包：

```bat
go build -o dji-mic-rx.exe .
```

## 驱动

启动后如果接收器已插上、但厂商接口还没有驱动，程序会弹窗询问是否安装（需要管理员权限）。

按顺序尝试：

1. **wdi-simple.exe**（如果找得到）：libwdi 的命令行安装器，它的驱动包带目录签名，Windows 最容易接受。本程序不附带它，请从 libwdi 项目获取，然后用驱动页的「选择 wdi-simple.exe…」指定一次，之后会被记住。
2. **Zadig**（如果找得到）：libwdi 的图形安装器，驱动包同样带签名目录。驱动页的手动步骤卡里写了该选什么（`Wireless Mic Rx` 的 `Interface 6`，驱动选 `WinUSB`）。
3. **内置驱动包**：程序会写出一个只引用 Windows 自带 `winusb.inf` 的 INF，用 `pnputil /add-driver … /install` 安装。

### 为什么内置那条路在本机走不通

Windows 不接受**没有签名目录**的第三方（OEM）驱动包，而程序自己写出的 INF 没有签名：`pnputil` 会在“导入驱动仓库”这一步失败，Windows 自己的设备安装日志 `C:\Windows\INF\setupapi.dev.log` 里把它记为 `Error = 0xE000022F`。所以本机实际可用的路是 Zadig（或 wdi-simple.exe）——它们的包由 libwdi 生成并带签名目录。

程序对这种失败的处理：认出原因、用中文写进界面和活动日志、把「用 Zadig 安装」摆在前面、并禁用重复尝试同一条路（再点也会被拒）。

日志会留在磁盘上，方便调试：

- 每次安装的输出：`%APPDATA%\dji-mic-rx\logs\driver-<时间戳>.log`（驱动页可打开文件夹或复制内容）
- 全部活动流水：`%APPDATA%\dji-mic-rx\logs\activity.log`

命令行也能跑同一条安装路径，并把日志直接打到终端：

```bat
go run ./cmd/djiprobe -install-driver     :: 安装（弹一次 UAC），打印完整日志
go run ./cmd/djiprobe -uninstall-driver   :: 移除驱动包
```

> 用 `go run` 启动程序时，提权子进程指向一个临时构建产物，Windows 有时无法正常启动它（表现就是日志是空的、安装好像“闪一下就没了”）。要试安装，请先 `go build`，再运行 `dji-mic-rx.exe` 或用 `djiprobe`。

安装完成后请**重新插拔接收器**，让 Windows 用新驱动重新枚举接口。

卸载：驱动页的「卸载驱动」用 `pnputil /delete-driver <oemNN.inf> /uninstall /force` 移除这个驱动包（编号从设备节点信息里读，不写死），之后接收器回到 Windows 的默认状态。

## 已知情况与限制

- **接口 6 只在重新枚举后出现**：本机实测（Windows 11 Build 26300）接收器在拔出重插之前只暴露 `MI_00`/`MI_01`，`MI_06` 节点完全不在设备树上；重插之后它才出现（同一端口、同一父设备实例）。所以**装完驱动必须再拔插一次**，接口才会被 WinUSB 接管。程序把这件事当作正常状态：每 1.5 秒扫一次设备树，接口出现就自动连接、消失就断开并写明原因、重新出现就自动重连（接收器重启也走这条路），活动日志里会逐条记录这些跳变。
- **协议版本**：v1 与 v2 固件的命令形状、CRC 种子与状态布局都不同，程序从状态流的版本标记识别，无需选择。
  - v1（初代 Mic Mini）不上报发射器与接收器电量，也没有「降噪开关」「按键切换降噪」「发射器自动关机」「音色」这些设置，界面上会写明原因而不是留一个空开关。
- **电量**：发射器报的是 1–7 的三位档位（1 满、7 即将关机），界面把它换算成百分比，属于估算；原始档位在状态页的说明里。
- **输入电平**：设备报的是它自己的单位，界面按 0–100 归一化并同时显示原始读数，不是 dBFS。
- **不接收帧的 CRC**：设备→主机的帧没有校验；两份参考实现也都不校验，文档里那条 ACK 的末两字节也不符合文档自己给的残差约定，所以程序只在**发送**时计算 CRC。
- **未实现的型号**：Mic Mini 2S RX（`0x4015`/`0x4115`，接口 4、alternate setting 1）在型号表里，但没有在真实硬件上验证过。表里没有的型号，程序会把它列出来、说明型号未知，并且**不会**自动安装驱动（不知道哪个接口带协议时给接口写驱动是有风险的）；`djiprobe -scan` 与「复制诊断信息」都会列出这些节点。
- **多个设备实例**：Windows 会记住见过的每个设备实例。拔插后新的实例上线、旧的留在注册表里成为“幽灵”，程序按“在线优先、在线中 WinUSB 优先”选定协议接口，不会把幽灵当成真设备（`internal/usb/enum_windows_test.go` 里有针对这种情况的用例）。
- **未实现的设置**：2S 的发射器内录相关命令（录音时间、循环录音等）没有实现。

## 代码结构

```
main.go                 启动窗口、菜单快捷键、提权助手的入口
internal/duml/          协议：帧、CRC-8/CRC-16、命令构造、v1/v2 解码、设置注册表
internal/usb/           设备树与驱动状态（注册表 + cfgmgr32）、WinUSB 读写、INF 与提权安装
internal/session/       把一个接收器管起来：扫描、打开、读状态流、发命令并等 ACK
internal/console/       窗口：主题、组件、状态页、设置页、设备页、驱动页、关于页
cmd/djiprobe/           硬件诊断命令行
cmd/uirender/           离屏渲染页面，用于查看与评审布局
screenshots/            uirender 的输出
```

界面与硬件之间只有 `console.Source` 一个接口，`internal/console/demo.go` 提供了一份示例实现，因此每个页面都能在没有接收器的情况下渲染、测试与查看。

## 协议来源

- [ShadowBitBasher/DJI-Mic-Control](https://github.com/ShadowBitBasher/DJI-Mic-Control) 的 `PROTOCOL.md`：帧格式、CRC、命令表、v1/v2 差异。本项目的协议实现按它编写，并用它给出的示例帧做了逐字节测试。
- [usokawa/dji-mic-mo](https://github.com/usokawa/dji-mic-mo)：字段偏移与位掩码的交叉核对来源（接收器电量、增益旋钮等）。
- [pbatard/libwdi](https://github.com/pbatard/libwdi)：Windows 上给厂商接口装 WinUSB 的既有做法（`wdi-simple.exe`、Zadig）。

## 界面上的取舍

- 颜色来自设备自己的指示灯：石墨底、琥珀（信号与选中）、翠绿（已连接）、珊瑚（告警）、蓝（充电）。
- 唯一“响”的元素是实时电平梯形表：它是屏幕上唯一会自己变化的东西，也是判断麦克风有没有在拾音最快的方式；其余部分保持安静——细线分隔、右对齐的数值、表格数字（tabular figures）。
- 深色与浅色两套配色都跟随系统；中文与拉丁数字共用系统字体，机器标识（序列号、INF、设备路径）用等宽字体。