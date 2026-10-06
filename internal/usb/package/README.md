# 已签名的驱动包放这里

这个目录存放**构建时签好名**的驱动包，它会用 `go:embed` 打进二进制。

- 放入：`dji_mic_rx_control.inf` 和 `dji_mic_rx_control.cat`（`.cat` 必须已签名）
- 生成方式：`scripts\build.cmd`（自签名或 `-Pfx` 指定公共 CA 证书），自签名时会多产出一个 `.cer`
- 有证书时签名要带时间戳，否则证书过期后新安装会失败

安装路径只有一条：程序在运行时用 `pnputil /add-driver` 把这个包导入（自签名的包顺带把
`.cer` 装进本机信任，卸载时移除）。用户机器上不需要 SDK、不需要 PowerShell 造证书。