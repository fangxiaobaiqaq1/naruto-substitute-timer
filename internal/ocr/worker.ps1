$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
[Console]::InputEncoding=[Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
function Write-Response($value) {
 [Console]::Out.WriteLine(($value | ConvertTo-Json -Depth 6 -Compress))
 [Console]::Out.Flush()
}
try {
 Add-Type -AssemblyName System.Runtime.WindowsRuntime
 [void][Windows.Graphics.Imaging.BitmapDecoder,Windows.Graphics.Imaging,ContentType=WindowsRuntime]
 [void][Windows.Graphics.Imaging.SoftwareBitmap,Windows.Graphics.Imaging,ContentType=WindowsRuntime]
 [void][Windows.Storage.Streams.InMemoryRandomAccessStream,Windows.Storage.Streams,ContentType=WindowsRuntime]
 [void][Windows.Storage.Streams.DataWriter,Windows.Storage.Streams,ContentType=WindowsRuntime]
 [void][Windows.Globalization.Language,Windows.Globalization,ContentType=WindowsRuntime]
 [void][Windows.Media.Ocr.OcrResult,Windows.Foundation,ContentType=WindowsRuntime]
 $asTask=([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.IsGenericMethod -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1' })[0]
 function Await($op,[Type]$type) {
  $task=$asTask.MakeGenericMethod($type).Invoke($null,@($op))
  try {$task.Wait()} catch {throw $task.Exception.ToString()}
  return $task.Result
 }
 $language=[Windows.Globalization.Language]::new('zh-Hans-CN')
 $engine=[Windows.Media.Ocr.OcrEngine,Windows.Foundation,ContentType=WindowsRuntime]::TryCreateFromLanguage($language)
 if($null -eq $engine){throw 'Windows simplified Chinese OCR language is not installed'}
 Write-Response @{ready=$true;language=$language.LanguageTag}
} catch {
 Write-Response @{ready=$false;error=$_.Exception.Message}
 exit 1
}
while($null -ne ($line=[Console]::In.ReadLine())) {
 $request=$null
 try {
  if($line.Length -gt 4194304){throw 'OCR request exceeds size limit'}
  $request=$line | ConvertFrom-Json
  $bytes=[Convert]::FromBase64String($request.image)
  if($bytes.Length -gt 3145728){throw 'OCR image exceeds size limit'}
  $stream=[Windows.Storage.Streams.InMemoryRandomAccessStream]::new()
  try {
   $writer=[Windows.Storage.Streams.DataWriter]::new($stream)
   try {
    $writer.WriteBytes($bytes)
    [void](Await ($writer.StoreAsync()) ([uint32]))
    [void]$writer.DetachStream()
   } finally {$writer.Dispose()}
   $stream.Seek(0)
   $decoder=Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
   $bitmap=Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
   try {
    if($bitmap.PixelWidth -gt [Windows.Media.Ocr.OcrEngine]::MaxImageDimension -or $bitmap.PixelHeight -gt [Windows.Media.Ocr.OcrEngine]::MaxImageDimension){throw 'OCR image dimensions exceed system limit'}
    $result=Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
    $lines=@($result.Lines | ForEach-Object { @{text=$_.Text;words=@($_.Words | ForEach-Object { @{text=$_.Text;x=$_.BoundingRect.X;y=$_.BoundingRect.Y;w=$_.BoundingRect.Width;h=$_.BoundingRect.Height} })} })
    Write-Response @{id=$request.id;lines=$lines}
   } finally {$bitmap.Dispose()}
  } finally {$stream.Dispose()}
 } catch {
  Write-Response @{id=$request.id;error=$_.Exception.Message}
 }
}
