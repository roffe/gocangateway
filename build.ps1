$env:CGO_ENABLED = "1" 
$env:GOGC = "100"
$env:CC = "x86_64-w64-mingw32-clang.exe"
$env:CXX = "x86_64-w64-mingw32-clang++.exe"


Write-Output "Building cangateway.exe"
$includes = @(
    'C:\Progra~2\Kvaser\Canlib\INC'
)

$libs = @(
    'C:\Progra~2\Kvaser\Canlib\Lib\MS'
)

$env:CGO_CFLAGS = ($includes | ForEach-Object { '-I' + $_ }) -join ' '
$env:CGO_LDFLAGS = ($libs | ForEach-Object { '-L' + $_ }) -join ' '
$env:GOARCH = "386"
go build -tags="canlib,j2534" -ldflags '-s -w -H=windowsgui' .

