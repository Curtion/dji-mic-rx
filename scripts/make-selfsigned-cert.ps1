# 为"自用/自签名"构建准备证书：在构建机上创建自签名代码签名证书，
# 并把它的公钥部分导出到 internal/usb/package/，于是 go build 会把它打进二进制。
#
#   powershell -ExecutionPolicy Bypass -File scripts/make-selfsigned-cert.ps1
#
# 之后跑（两者都会自动按主题名找到这张证书）：
#   powershell -ExecutionPolicy Bypass -File scripts/sign-package.ps1
#   go build -o dji-mic-rx.exe .
#
# 用户机器上：程序在安装时会把这个 .cer 装进 Root + TrustedPublisher（in-box 的
# certutil，不需要 SDK），卸载时会移除。若换成公共 CA 签发，就不要导出 .cer：
# 那时签名链本来就受信任，用户机器无需任何信任变更。

param(
  [string]$Subject = 'CN=DJI Mic Control (self-signed)',
  [string]$OutDir
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (-not $OutDir) { $OutDir = Join-Path $root 'internal\usb\package' }
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$cert = Get-ChildItem Cert:\LocalMachine\My |
  Where-Object { $_.Subject -eq $Subject -and $_.HasPrivateKey } |
  Sort-Object NotAfter -Descending | Select-Object -First 1

if (-not $cert) {
  Write-Host "创建自签名代码签名证书：$Subject"
  $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject $Subject `
    -CertStoreLocation Cert:\LocalMachine\My -KeyExportPolicy Exportable `
    -KeyLength 2048 -KeyAlgorithm RSA -HashAlgorithm SHA256 `
    -NotAfter (Get-Date).AddYears(10)
} else {
  Write-Host "复用已有证书：$($cert.Thumbprint)"
}

$cerPath = Join-Path $OutDir 'dji_mic_rx_control.cer'
Export-Certificate -Cert $cert -FilePath $cerPath -Force | Out-Null
Write-Host "已导出公钥：$cerPath"
Write-Host "指纹：$($cert.Thumbprint)"
Write-Host ""
Write-Host "接着运行 scripts/sign-package.ps1 签出目录文件，然后 go build。"