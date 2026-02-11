$temp_dir = ".\setup_temp"
$llvm = "https://github.com/mstorsjo/llvm-mingw/releases/download/20251007/llvm-mingw-20251007-ucrt-x86_64.zip"

# create directory temp if not existing
if (-not (Test-Path -Path "$temp_dir")) {
    Write-Output "Creating temporary directory..."
    New-Item -ItemType Directory -Path "$temp_dir" | Out-Null
}

# download llvm-mingw
Write-Output "Downloading llvm-mingw"
Invoke-WebRequest -Uri $llvm -OutFile "$temp_dir\llvm-mingw.zip"

# Write-Output "Extracting llvm-MinGW"
Expand-Archive -Path "$temp_dir\llvm-mingw.zip" -DestinationPath ".\" -Force

# rename folder llvm-mingw-20251007-ucrt-x86_64 to llvm-mingw
Write-Output "Renaming llvm-mingw folder"
Rename-Item -Path ".\llvm-mingw-20251007-ucrt-x86_64" -NewName "llvm-mingw"

Write-Output "Cleaning up"
Remove-Item -Recurse -Force -Path $temp_dir