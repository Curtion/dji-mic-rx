# 已签名的驱动包放这里

这个目录存放**构建时签好名**的驱动包，它会用 `go:embed` 打进二进制。

- 放入：`dji_mic_rx_control.inf` 和 `dji_mic_rx_control.cat`（`.cat` 必须已签名）
- 生成方式：`powershell -ExecutionPolicy Bypass -File scripts/sign-package.ps1 -Pfx <你的代码签名证书.pfx> -PfxPassword <密码>`
- 有证书时签名要带时间戳，否则证书过期后新安装会失败

**目录为空时**程序仍可运行：它会退回到"开发/自用模式"（运行时自签名，需要 Windows SDK 的
makecat/signtool，并且会往系统信任里装一张自签名证书），或者引导使用 Zadig。

正常分发应当只放**已签名**的包：用户机器上不需要 SDK、不需要 PowerShell 造证书、
不需要改动系统信任根，运行时只是 `pnputil /add-driver` 把它导入。