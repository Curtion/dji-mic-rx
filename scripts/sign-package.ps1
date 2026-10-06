# Build-time signing of the driver package.
#
# Run this once on the machine that holds the code signing certificate. It
# writes the signed package into internal/usb/package/, which the app embeds
# with go:embed, so end users only import it: no SDK, no PowerShell certificate
# work, no change to their trust stores.
#
#   powershell -ExecutionPolicy Bypass -File scripts/sign-package.ps1 `
#       -Pfx C:\path\codesign.pfx -PfxPassword secret
#
# Without -Pfx it signs with a certificate already in the machine's store,
# chosen by -Thumbprint or by subject (-Subject). The default subject is the one
# scripts/make-selfsigned-cert.ps1 creates, so the self-signed route needs no
# arguments; a token, cloud or purchased certificate is selected with
# -Thumbprint, -Pfx, or -Subject.
#
# Timestamping with -TimestampServer keeps the signature valid after the
# certificate expires, so an existing install keeps working and updates can
# still be verified. Pass -TimestampServer '' to skip it.

param(
  [string]$Pfx,
  [string]$PfxPassword,
  [string]$Thumbprint,
  [string]$Subject = 'CN=DJI Mic Control (self-signed)',
  [string]$TimestampServer = 'http://timestamp.digicert.com',
  [string]$MakeCat,
  [string]$SignTool
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$outDir = Join-Path $root 'internal\usb\package'
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("dji-package-" + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $work, $outDir | Out-Null

function Find-SdkTool([string]$name, [string]$given) {
  if ($given) { return $given }
  $found = Get-ChildItem 'C:\Program Files (x86)\Windows Kits\10\bin' -Directory -ErrorAction SilentlyContinue |
    Sort-Object Name -Descending |
    ForEach-Object { Join-Path $_.FullName "x64\$name" } |
    Where-Object { Test-Path $_ } |
    Select-Object -First 1
  if (-not $found) { throw "找不到 $name，请用 -MakeCat/-SignTool 指定路径（需要 Windows SDK）" }
  return $found
}

$makecat = Find-SdkTool 'makecat.exe' $MakeCat
$signtool = Find-SdkTool 'signtool.exe' $SignTool

# 1. the INF, rendered the same way the app renders it
$inf = @'
; DJI Mic 接收器控制台 — WinUSB for the receiver's vendor interface
;
; 只给厂商接口绑定 Microsoft 自带的 WinUSB 驱动。接收器的音频接口与按键接口
; 保持系统驱动，录音不受影响。

[Version]
Signature   = "$Windows NT$"
Class       = USBDevice
ClassGuid   = {88BAE032-5A81-49f0-BC3D-A4FF138216D6}
Provider    = %ProviderName%
CatalogFile = dji_mic_rx_control.cat
DriverVer   = 01/01/2026,1.0.0.0

[Manufacturer]
%ProviderName% = DJIMic, NTamd64, NTarm64

[DJIMic.NTamd64]
%DeviceName% = DJIMic_Install, USB\VID_2CA3&PID_4011&MI_06

[DJIMic.NTarm64]
%DeviceName% = DJIMic_Install, USB\VID_2CA3&PID_4011&MI_06

[DJIMic_Install]
Include = winusb.inf
Needs   = WINUSB.NT

[DJIMic_Install.HW]
Include = winusb.inf
Needs   = WINUSB.NT.HW
AddReg  = DJIMic_AddReg

[DJIMic_AddReg]
HKR,,DeviceInterfaceGUIDs,0x10000,"{c4f1c6a9-1f4e-4b2c-9f7b-0d5c2a7e4b31}","{a5dcbf10-6530-11d2-901f-00c04fb951ed}"

[DJIMic_Install.Services]
Include = winusb.inf
Needs   = WINUSB.NT.Services

[Strings]
ProviderName = "DJI Mic Control"
DeviceName   = "DJI Mic Receiver control interface"
'@
$infPath = Join-Path $work 'dji_mic_rx_control.inf'
Set-Content -LiteralPath $infPath -Value $inf -Encoding UTF8

# 2. the catalog definition, then the catalog itself. makecat resolves the
#    member names against its working directory, so run it inside $work.
$cdf = @"
[CatalogHeader]
Name=dji_mic_rx_control.cat
PublicVersion=0x0000001
EncodingType=0x00010001
CATATTR1=0x10010001:OSAttr:2:6.1,6.2,6.3,6.4,10.0

[CatalogFiles]
<hash>dji_mic_rx_control.inf=dji_mic_rx_control.inf
"@
Set-Content -LiteralPath (Join-Path $work 'dji_mic_rx_control.cdf') -Value $cdf -Encoding ASCII

Push-Location $work
try {
  & $makecat -v 'dji_mic_rx_control.cdf' | Write-Host
} finally {
  Pop-Location
}
if (-not (Test-Path (Join-Path $work 'dji_mic_rx_control.cat'))) { throw 'makecat 没有生成目录文件' }

# 3. sign the catalog
$catPath = Join-Path $work 'dji_mic_rx_control.cat'
$signArgs = @('sign', '/v', '/fd', 'sha256')
if ($Pfx) {
  $signArgs += @('/f', $Pfx)
  if ($PfxPassword) { $signArgs += @('/p', $PfxPassword) }
} elseif ($Thumbprint) {
  $signArgs += @('/sha1', $Thumbprint, '/sm')
} else {
  $signArgs += @('/n', $Subject, '/sm')
}
if ($TimestampServer) { $signArgs += @('/tr', $TimestampServer, '/td', 'sha256') }
$signArgs += $catPath
& $signtool @signArgs
if ($LASTEXITCODE -ne 0) { throw "signtool 失败（退出码 $LASTEXITCODE）" }

# 4. verify, then publish into the module so the next build embeds it
& $signtool verify /pa /v /c $catPath $infPath
if ($LASTEXITCODE -ne 0) { throw "签名校验失败（退出码 $LASTEXITCODE）" }

Copy-Item -LiteralPath $infPath -Destination $outDir -Force
Copy-Item -LiteralPath $catPath -Destination $outDir -Force
Write-Host ""
Write-Host "已写入 $outDir："
Get-ChildItem $outDir -Filter 'dji_mic_rx_control.*' | ForEach-Object { Write-Host ("  " + $_.Name + "  " + $_.Length + " bytes") }
Write-Host "接着 go build 就会把它打进二进制；用户侧只需要 pnputil 导入。"