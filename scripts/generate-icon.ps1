#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing

$root = Split-Path -Parent $PSScriptRoot
$assets = Join-Path $root 'assets'
New-Item -ItemType Directory -Force -Path $assets | Out-Null
$sizes = @(16, 20, 24, 32, 40, 48, 64, 128, 256)
$images = @()
foreach ($size in $sizes) {
  $bitmap = [System.Drawing.Bitmap]::new($size, $size)
  $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
  $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
  $graphics.ScaleTransform($size / 256.0, $size / 256.0)
  $background = [System.Drawing.SolidBrush]::new([System.Drawing.ColorTranslator]::FromHtml('#15191b'))
  $foreground = [System.Drawing.Pen]::new([System.Drawing.ColorTranslator]::FromHtml('#e6eeeb'), 13)
  $foreground.LineJoin = [System.Drawing.Drawing2D.LineJoin]::Round
  $green = [System.Drawing.SolidBrush]::new([System.Drawing.ColorTranslator]::FromHtml('#59d9a0'))
  $plate = [System.Drawing.Drawing2D.GraphicsPath]::new()
  $body = [System.Drawing.Drawing2D.GraphicsPath]::new()
  try {
    $plate.AddArc(8, 8, 40, 40, 180, 90)
    $plate.AddArc(208, 8, 40, 40, 270, 90)
    $plate.AddArc(208, 208, 40, 40, 0, 90)
    $plate.AddArc(8, 208, 40, 40, 90, 90)
    $plate.CloseFigure()
    $graphics.FillPath($background, $plate)
    $graphics.DrawRectangle($foreground, 195, 110, 27, 36)
    $body.AddArc(35, 78, 36, 36, 180, 90)
    $body.AddArc(161, 78, 36, 36, 270, 90)
    $body.AddArc(161, 142, 36, 36, 0, 90)
    $body.AddArc(35, 142, 36, 36, 90, 90)
    $body.CloseFigure()
    $graphics.FillPath($background, $body)
    $graphics.DrawPath($foreground, $body)
    foreach ($x in @(68, 108, 148)) { $graphics.FillEllipse($green, $x, 112, 16, 16) }
    $stream = [System.IO.MemoryStream]::new()
    try {
      $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
      $images += ,$stream.ToArray()
    } finally { $stream.Dispose() }
  } finally {
    $body.Dispose(); $plate.Dispose(); $green.Dispose(); $foreground.Dispose()
    $background.Dispose(); $graphics.Dispose(); $bitmap.Dispose()
  }
}

$file = [System.IO.File]::Create((Join-Path $assets 'app.ico'))
$writer = [System.IO.BinaryWriter]::new($file)
try {
  $writer.Write([uint16]0); $writer.Write([uint16]1); $writer.Write([uint16]$sizes.Count)
  $offset = 6 + 16 * $sizes.Count
  for ($i = 0; $i -lt $sizes.Count; $i++) {
    $dimension = $sizes[$i] % 256
    $writer.Write([byte]$dimension); $writer.Write([byte]$dimension)
    $writer.Write([byte]0); $writer.Write([byte]0)
    $writer.Write([uint16]1); $writer.Write([uint16]32)
    $writer.Write([uint32]$images[$i].Length); $writer.Write([uint32]$offset)
    $offset += $images[$i].Length
  }
  foreach ($image in $images) { $writer.Write([byte[]]$image) }
} finally { $writer.Dispose() }
