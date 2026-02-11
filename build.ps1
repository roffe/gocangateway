$env:CGO_ENABLED = "1" 
$env:GOGC = "100"
$env:CC = "x86_64-w64-mingw32-clang.exe"
$env:CXX = "x86_64-w64-mingw32-clang++.exe"


Write-Output "Building cangateway.exe"
$env:GOARCH = "386"
go build -tags="j2534" -ldflags '-s -w -H=windowsgui' -o cangateway.exe .

