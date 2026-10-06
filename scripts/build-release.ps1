#Requires -Version 5.1
<#
一次性把"驱动包签名"和"构建 exe"一起做完：

  scripts\build.cmd                      :: 最省事，双击或 cmd 里跑
  powershell -ExecutionPolicy Bypass -File scripts\build-release.ps1

做的事：
  1. 在当前用户证书库里找到（没有就创建）自签名代码签名证书
  2. 用 Windows SDK 的 makecat 生成目录文件，signtool 签名
  3. 把 .inf / .cat（自签名时还有 .cer）放进 internal\usb\package\，
     下一步 go build 会把它们嵌进 exe
  4. go build -o dji-mic-rx.exe

用户机器上：程序安装驱动时只做 pnputil 导入；自签名的包会顺带把 .cer
装进本机信任（in-box 的 certutil），卸载时移除。用公共 CA 证书签名时
（-Pfx 或 -Thumbprint）不会导出 .cer，用户机器无需任何信任变更。

可选参数：
  -Pfx <文件> -PfxPassword <密码>   用 PFX 里的证书签名（公共 CA 或自购证书）
  -Thumbprint <指纹>                用证书库里的指定证书
  -Subject <主题>                   默认 CN=DJI Mic Control (self-signed)
  -TimestampServer <URL>            默认 http://timestamp.digicert.com
  -NoTimestamp                      不加时间戳（离线时用）
  -SkipBuild                        只签名，不构建 exe
#>
[CmdletBinding()]
param(
  [string]$Pfx,
  [string]$PfxPassword,
  [string]$Thumbprint,
  [string]$Subject = 'CN=DJI Mic Control (self-signed)',
  [string]$TimestampServer = 'http://timestamp.digicert.com',
  [switch]$NoTimestamp,
  [switch]$SkipBuild
)

# PowerShell 7 没有 PKI 模块和 Cert: 盘，遇到就换回 Windows PowerShell 5.1 重跑。
if ($PSVersionTable.PSVersion.Major -ge 7) {
  $ps51 = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
  if (Test-Path $ps51) {
    Write-Host "检测到 PowerShell 7，改用 Windows PowerShell 5.1 运行…"
    & $ps51 -NoProfile -ExecutionPolicy Bypass -File $PSCommandPath @args
    exit $LASTEXITCODE
  }
}

$ErrorActionPreference = 'Stop'

# 校验自签名包时 signtool 会把「证书链不被信任」写到错误流，而本机确实还没信任它；
# 校验本身不能被当成终止错误。
# 外部工具的退出码在后面逐个显式检查，所以这里不用 Stop：
# 自签名包校验时 signtool 会把「证书链不被信任」写到 stderr，Stop 会把它当成终止错误。
$ErrorActionPreference = 'Continue'
$env:PSModulePath = @(
  (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\Modules'),
  (Join-Path $env:ProgramFiles 'WindowsPowerShell\Modules')
) -join ';'
Import-Module PKI -ErrorAction Stop

$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$outDir = Join-Path $root 'internal\usb\package'
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("dji-package-" + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $work, $outDir | Out-Null

function Find-SdkTool([string]$name) {
  $found = Get-ChildItem 'C:\Program Files (x86)\Windows Kits\10\bin' -Directory -ErrorAction SilentlyContinue |
    Sort-Object Name -Descending |
    ForEach-Object { Join-Path $_.FullName "x64\$name" } |
    Where-Object { Test-Path $_ } |
    Select-Object -First 1
  if (-not $found) {
    throw "找不到 $name：签名需要 Windows SDK（本机未安装时请改用 Zadig 安装驱动）"
  }
  return $found
}

Write-Host "== 1/4 准备签名证书" -ForegroundColor Cyan
$useSelfSigned = $false
if ($Pfx) {
  Write-Host "   用 PFX：$Pfx"
} elseif ($Thumbprint) {
  Write-Host "   用证书库里指纹为 $Thumbprint 的证书"
} else {
  $cert = Get-ChildItem Cert:\CurrentUser\My |
    Where-Object { $_.Subject -eq $Subject -and $_.HasPrivateKey } |
    Sort-Object NotAfter -Descending | Select-Object -First 1
  if ($cert) {
    Write-Host "   复用已有自签名证书：$($cert.Thumbprint)"
  } else {
    Write-Host "   创建自签名证书：$Subject"
    $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject $Subject `
      -CertStoreLocation Cert:\CurrentUser\My -KeyExportPolicy Exportable `
      -KeyLength 2048 -KeyAlgorithm RSA -HashAlgorithm SHA256 `
      -NotAfter (Get-Date).AddYears(10)
    Write-Host "   指纹：$($cert.Thumbprint)"
  }
  $Thumbprint = $cert.Thumbprint
  $cerPath = Join-Path $outDir 'dji_mic_rx_control.cer'
  Export-Certificate -Cert $cert -FilePath $cerPath -Force | Out-Null
  $useSelfSigned = $true
  Write-Host "   已导出公钥（会随 exe 发布，安装时装入本机信任）：$cerPath"
}

$makecat = Find-SdkTool 'makecat.exe'
$signtool = Find-SdkTool 'signtool.exe'

Write-Host "== 2/4 生成目录文件并签名" -ForegroundColor Cyan
$infPath = Join-Path $work 'dji_mic_rx_control.inf'
$infTemplate = Join-Path $outDir 'dji_mic_rx_control.inf'
if (-not (Test-Path $infTemplate)) {
  # The INF text lives in the app, so ask for it rather than keeping a second
  # copy here that would drift.
  Write-Host "   生成 INF 模板（dji-mic-rx -write-inf）"
  Push-Location $root
  try {
    & go run ./cmd/djiprobe -write-inf "internal/usb/package"
    if ($LASTEXITCODE -ne 0) { throw "生成 INF 失败（退出码 $LASTEXITCODE）" }
  } finally { Pop-Location }
}
Copy-Item -LiteralPath $infTemplate -Destination $infPath -Force
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
try { & $makecat -v 'dji_mic_rx_control.cdf' | Write-Host } finally { Pop-Location }
$catPath = Join-Path $work 'dji_mic_rx_control.cat'
if (-not (Test-Path $catPath)) { throw 'makecat 没有生成目录文件' }

$signArgs = @('sign', '/v', '/fd', 'sha256')
if ($Pfx) {
  $signArgs += @('/f', $Pfx)
  if ($PfxPassword) { $signArgs += @('/p', $PfxPassword) }
} else {
  # 不给 /sm 就是当前用户证书库，因此构建不需要管理员权限。
  $signArgs += @('/sha1', $Thumbprint)
}
if (-not $NoTimestamp -and $TimestampServer) { $signArgs += @('/tr', $TimestampServer, '/td', 'sha256') }
$signArgs += $catPath
& $signtool @signArgs
if ($LASTEXITCODE -ne 0) { throw "signtool 失败（退出码 $LASTEXITCODE）" }

$verify = & $signtool verify /pa /c $catPath $infPath 2>&1
$verifyCode = $LASTEXITCODE
if ($verifyCode -ne 0) {
  if ($useSelfSigned) {
    Write-Host "   提示：本机还没信任这张自签名证书，所以校验会报不受信任；" -ForegroundColor Yellow
    Write-Host "        目标机器由程序在安装时装入信任（并会在卸载时移除），这里不算失败。" -ForegroundColor Yellow
  } else {
    $verify | Write-Host
    throw "签名校验失败：证书链不被信任，请检查证书或时间戳"
  }
} else {
  Write-Host "   签名校验通过"
}

Write-Host "== 3/4 把包放进 internal\usb\package" -ForegroundColor Cyan
Copy-Item -LiteralPath $catPath -Destination $outDir -Force
Get-ChildItem $outDir -Filter 'dji_mic_rx_control.*' | ForEach-Object {
  Write-Host ("   " + $_.Name + "  " + $_.Length + " bytes")
}

if ($SkipBuild) {
  Write-Host "== 完成（-SkipBuild：没有构建 exe）" -ForegroundColor Green
  exit 0
}

Write-Host "== 4/4 构建 exe" -ForegroundColor Cyan
Push-Location $root
try {
  & go build -o dji-mic-rx.exe .
  if ($LASTEXITCODE -ne 0) { throw "go build 失败（退出码 $LASTEXITCODE）" }
} finally { Pop-Location }

$exe = Join-Path $root 'dji-mic-rx.exe'
Write-Host ""
Write-Host "完成：$exe（$([math]::Round((Get-Item $exe).Length / 1MB, 1)) MB）" -ForegroundColor Green
if ($useSelfSigned) {
  Write-Host "这个 exe 内置了自签名证书签名的驱动包：目标机器安装驱动时会把证书装进本机信任，卸载时移除。"
} else {
  Write-Host "这个 exe 内置了由受信任机构签名的驱动包：目标机器安装驱动时无需任何信任变更。"
}