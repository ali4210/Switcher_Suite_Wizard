@echo off
setlocal EnableExtensions EnableDelayedExpansion
chcp 437 >nul

REM ==============================================================================
REM SWITCHER SUITE WIZARD - ENTERPRISE SELF-HEALING MULTI-OS BUILD WRAPPER
REM
REM Usage:
REM   autorun.bat           Interactive Multi-OS selector, build, and elevated launch
REM   autorun.bat -f        Force rebuild: clean build/test cache + remove old EXE
REM   autorun.bat --hard    Full Go cache purge and fresh clean rebuild
REM   autorun.bat --help    Display help screen
REM ==============================================================================

set "PROJECT_DIR=%~dp0"
set "LOCAL_GO_DIR=%PROJECT_DIR%.go_runtime"
set "GO_BOOTSTRAP_VERSION=1.24.0"
set "GO_ZIP_URL=https://go.dev/dl/go%GO_BOOTSTRAP_VERSION%.windows-amd64.zip"
set "MODE=%~1"

cd /d "%PROJECT_DIR%"

REM ------------------------------------------------------------------------------
REM ANSI Terminal Colors
REM ------------------------------------------------------------------------------
for /F %%A in ('echo prompt $E ^| cmd') do set "ESC=%%A"

set "RED=%ESC%[0;31m"
set "GREEN=%ESC%[0;32m"
set "YELLOW=%ESC%[1;33m"
set "CYAN=%ESC%[0;36m"
set "BOLD=%ESC%[1m"
set "DIM=%ESC%[2m"
set "NC=%ESC%[0m"

REM ------------------------------------------------------------------------------
REM Argument Pre-Check: Help
REM ------------------------------------------------------------------------------
if /I "%MODE%"=="-h" goto :show_help
if /I "%MODE%"=="--help" goto :show_help
if /I "%MODE%"=="help" goto :show_help
if /I "%MODE%"=="-?" goto :show_help
goto :display_wrapper_menu

:show_help
cls
echo.
echo %BOLD%Usage:%NC%
echo   autorun.bat            Interactive Multi-OS selector, build, and launch
echo   autorun.bat -f         Force rebuild with build/test cache cleanup
echo   autorun.bat --hard     Full Go cache purge and fresh rebuild
echo   autorun.bat --help     Display this help screen
echo.
echo %BOLD%Features:%NC%
echo   =^> Multi-OS Target Wrapper: Interactive platform target selection.
echo   =^> High-Speed Extraction: Uses native tar/curl engines for instant setup.
echo   =^> Self-Healing Go Runtime: Auto-provisions portable toolchain if missing.
echo   =^> Offline Resilient: Compiles directly via vendor directory if offline.
echo   =^> Native Elevation: Automatically handles Windows Administrator rights.
echo   =^> Zero-Latency Global CLI: 'switcher-wizard' directly accesses native binary.
echo.
pause
exit /b 0

REM ------------------------------------------------------------------------------
REM Step 1: Interactive Multi-OS Target Selection Wrapper with Favorite Art
REM ------------------------------------------------------------------------------
:display_wrapper_menu
cls

REM ASCII Portrait Renderer - pulled from a separate file so cmd.exe never
REM has to parse the art text as commands (avoids all %/encoding pitfalls)
if exist "%PROJECT_DIR%banner_wrapper.txt" (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-Content -LiteralPath '%PROJECT_DIR%banner_wrapper.txt' | Write-Host -ForegroundColor Cyan"
) else (
    echo %YELLOW%[!]%NC% banner_wrapper.txt not found - skipping ASCII art.
)
echo.

echo %CYAN%+============================================================================+%NC%
echo %CYAN%^|%NC% %BOLD%TARGET OPERATING SYSTEM SELECTION WRAPPER%NC%                                  %CYAN%^|%NC%
echo %CYAN%+============================================================================+%NC%
echo %CYAN%^|%NC% [1] =^> %GREEN%Linux (Debian / Ubuntu / Kali / RHEL / CentOS / Arch)%NC%               %CYAN%^|%NC%
echo %CYAN%^|%NC% [2] =^> %GREEN%Microsoft Windows (x64 / Win10 / Win11 / Server)%NC%                    %CYAN%^|%NC%
echo %CYAN%^|%NC% [3] =^> %GREEN%Apple macOS (Darwin Subsystem / amd64 ^& arm64)%NC%                      %CYAN%^|%NC%
echo %CYAN%^|%NC% [4] =^> %YELLOW%Auto-Detect Host Platform ^& Launch Immediately%NC%                      %CYAN%^|%NC%
echo %CYAN%^|%NC% [Q] =^> Exit Build System                                                   %CYAN%^|%NC%
echo %CYAN%+============================================================================+%NC%
set /p "OS_CHOICE= Select Target Operating System [1-4 / Q]: "

REM --------------------------------------------------------------------------
REM FIX: choices now match the menu labels above.
REM   1 = Linux, 2 = Windows, 3 = macOS  (previously 1 and 2 were swapped)
REM --------------------------------------------------------------------------
if /i "%OS_CHOICE%"=="1" (
    set "TARGET_OS=linux"
    set "TARGET_ARCH=amd64"
    set "BINARY_NAME=Switcher_Suite_Wizard"
    goto :bootstrap_go
)
if /i "%OS_CHOICE%"=="2" (
    set "TARGET_OS=windows"
    set "TARGET_ARCH=amd64"
    set "BINARY_NAME=Switcher_Suite_Wizard.exe"
    goto :bootstrap_go
)
if /i "%OS_CHOICE%"=="3" (
    set "TARGET_OS=darwin"
    set "TARGET_ARCH=amd64"
    set "BINARY_NAME=Switcher_Suite_Wizard_macOS"
    goto :bootstrap_go
)
if /i "%OS_CHOICE%"=="4" (
    set "TARGET_OS=windows"
    set "TARGET_ARCH=amd64"
    set "BINARY_NAME=Switcher_Suite_Wizard.exe"
    goto :bootstrap_go
)
if /i "%OS_CHOICE%"=="Q" (
    echo.
    echo %YELLOW%=^> Operation cancelled by user.%NC%
    exit /b 0
)

echo.
echo %RED%[!] Invalid selection. Defaulting to Windows.%NC%
set "TARGET_OS=windows"
set "TARGET_ARCH=amd64"
set "BINARY_NAME=Switcher_Suite_Wizard.exe"

REM ------------------------------------------------------------------------------
REM Step 2: Self-Healing Go Toolchain Discovery & High-Speed Auto-Provisioning
REM ------------------------------------------------------------------------------
:bootstrap_go
echo.
echo %BOLD%%CYAN%--- CHECKING GO TOOLCHAIN ---%NC%

REM Check 1: System PATH
where go >nul 2>nul
if %errorlevel% equ 0 (
    set "GO_BIN=go"
    set "GOFMT_BIN=gofmt"
    for /f "delims=" %%G in ('go version') do set "GO_VERSION_STR=%%G"
    echo %GREEN%[OK]%NC% System Go compiler found: !GO_VERSION_STR!
    goto :verify_environment
)

REM Check 2: Standard Program Files
if exist "C:\Program Files\Go\bin\go.exe" (
    set "GOROOT=C:\Program Files\Go"
    set "PATH=C:\Program Files\Go\bin;!PATH!"
    set "GO_BIN=C:\Program Files\Go\bin\go.exe"
    set "GOFMT_BIN=C:\Program Files\Go\bin\gofmt.exe"
    for /f "delims=" %%G in ('"C:\Program Files\Go\bin\go.exe" version') do set "GO_VERSION_STR=%%G"
    echo %GREEN%[OK]%NC% Program Files Go compiler detected: !GO_VERSION_STR!
    goto :verify_environment
)

REM Check 3: Standard Root Path
if exist "C:\Go\bin\go.exe" (
    set "GOROOT=C:\Go"
    set "PATH=C:\Go\bin;!PATH!"
    set "GO_BIN=C:\Go\bin\go.exe"
    set "GOFMT_BIN=C:\Go\bin\gofmt.exe"
    for /f "delims=" %%G in ('"C:\Go\bin\go.exe" version') do set "GO_VERSION_STR=%%G"
    echo %GREEN%[OK]%NC% C:\Go compiler detected: !GO_VERSION_STR!
    goto :verify_environment
)

REM Check 4: Local Portable Runtime Sandbox
if exist "%LOCAL_GO_DIR%\bin\go.exe" (
    set "GOROOT=%LOCAL_GO_DIR%"
    set "PATH=%LOCAL_GO_DIR%\bin;!PATH!"
    set "GO_BIN=%LOCAL_GO_DIR%\bin\go.exe"
    set "GOFMT_BIN=%LOCAL_GO_DIR%\bin\gofmt.exe"
    for /f "delims=" %%G in ('"%LOCAL_GO_DIR%\bin\go.exe" version') do set "GO_VERSION_STR=%%G"
    echo %GREEN%[OK]%NC% Portable Go runtime active: !GO_VERSION_STR!
    goto :verify_environment
)

REM Fallback: Automated Rapid Toolchain Download and Ultra-Fast Unpacking
echo %YELLOW%[!]%NC% No Go compiler detected in PATH or system folders.
echo %CYAN%[+]%NC% Auto-provisioning Go %GO_BOOTSTRAP_VERSION% portable toolchain...

if not exist "%PROJECT_DIR%.cache" mkdir "%PROJECT_DIR%.cache"
set "ZIP_DEST=%PROJECT_DIR%.cache\go_temp.zip"

where curl.exe >nul 2>nul
if %errorlevel% equ 0 (
    echo %CYAN%[+]%NC% Downloading archive with native high-speed curl...
    curl.exe -L --progress-bar "%GO_ZIP_URL%" -o "%ZIP_DEST%"
) else (
    echo %CYAN%[+]%NC% Downloading archive via PowerShell .NET stream...
    powershell -NoProfile -ExecutionPolicy Bypass -Command ^
        "[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; " ^
        "$wc = New-Object System.Net.WebClient; $wc.DownloadFile('%GO_ZIP_URL%', '%ZIP_DEST%');"
)

if not exist "%ZIP_DEST%" (
    echo.
    echo %RED%[X]%NC% Failed to download Go toolchain.
    echo %YELLOW%[!]%NC% Check internet connectivity or install Go manually from https://go.dev/dl/
    echo.
    pause
    exit /b 1
)

echo %CYAN%[+]%NC% Fast-extracting portable Go compiler...

if exist "%LOCAL_GO_DIR%" rmdir /s /q "%LOCAL_GO_DIR%"

where tar.exe >nul 2>nul
if %errorlevel% equ 0 (
    tar.exe -xf "%ZIP_DEST%" -C "%PROJECT_DIR%.cache"
    move "%PROJECT_DIR%.cache\go" "%LOCAL_GO_DIR%" >nul 2>nul
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -Command ^
        "Add-Type -AssemblyName System.IO.Compression.FileSystem; " ^
        "[System.IO.Compression.ZipFile]::ExtractToDirectory('%ZIP_DEST%', '%PROJECT_DIR%.cache'); " ^
        "Move-Item -Path '%PROJECT_DIR%.cache\go' -Destination '%LOCAL_GO_DIR%' -Force;"
)

rmdir /s /q "%PROJECT_DIR%.cache" >nul 2>nul

if not exist "%LOCAL_GO_DIR%\bin\go.exe" (
    echo.
    echo %RED%[X]%NC% Extraction failed or archive was corrupt.
    echo.
    pause
    exit /b 1
)

set "GOROOT=%LOCAL_GO_DIR%"
set "PATH=%LOCAL_GO_DIR%\bin;!PATH!"
set "GO_BIN=%LOCAL_GO_DIR%\bin\go.exe"
set "GOFMT_BIN=%LOCAL_GO_DIR%\bin\gofmt.exe"

for /f "delims=" %%G in ('"%GO_BIN%" version') do set "GO_VERSION_STR=%%G"
echo %GREEN%[OK]%NC% Self-healing complete. Provisioned: !GO_VERSION_STR!

:verify_environment
if not exist "go.mod" (
    echo %YELLOW%[!]%NC% go.mod not found. Initializing module 'switcher'...
    "%GO_BIN%" mod init switcher >nul 2>nul
)
echo %GREEN%[OK]%NC% Go module integrity confirmed.

REM ------------------------------------------------------------------------------
REM Step 3: Build Mode Cache Router
REM ------------------------------------------------------------------------------
if "%MODE%"=="" goto :execute_normal
if /I "%MODE%"=="-f" goto :execute_force
if /I "%MODE%"=="--force" goto :execute_force
if /I "%MODE%"=="--hard" goto :execute_hard

echo.
echo %RED%[X]%NC% Unknown build parameter: %MODE%
goto :show_help

:execute_normal
echo.
echo %BOLD%%CYAN%--- NORMAL BUILD MODE ---%NC%
echo %CYAN%[+]%NC% Utilizing compiler caches for rapid execution.
goto :run_build_pipeline

:execute_force
echo.
echo %BOLD%%CYAN%--- FORCE REBUILD MODE ---%NC%
echo %YELLOW%[!]%NC% Purging previous binaries and cleaning build/test cache...

call :kill_running_instance

if exist "%BINARY_NAME%" (
    del /f /q "%BINARY_NAME%"
    if errorlevel 1 (
        echo %RED%[X]%NC% Could not remove %BINARY_NAME%. File is in use.
        pause
        exit /b 1
    )
)

"%GO_BIN%" clean -cache -testcache
echo %GREEN%[OK]%NC% Force cache purge complete.
goto :run_build_pipeline

:execute_hard
echo.
echo %BOLD%%CYAN%--- HARD PURGE MODE ---%NC%
echo %YELLOW%[!]%NC% Executing full cache, test, and fuzz purge...

call :kill_running_instance

if exist "%BINARY_NAME%" (
    del /f /q "%BINARY_NAME%"
    if errorlevel 1 (
        echo %RED%[X]%NC% Could not delete %BINARY_NAME%.
        pause
        exit /b 1
    )
)

echo %CYAN%[+]%NC% Cleaning Go build cache...
"%GO_BIN%" clean -cache
echo %CYAN%[+]%NC% Cleaning Go test cache...
"%GO_BIN%" clean -testcache
echo %CYAN%[+]%NC% Cleaning Go fuzz cache...
"%GO_BIN%" clean -fuzzcache
echo %GREEN%[OK]%NC% Hard purge completed cleanly.
goto :run_build_pipeline

REM ------------------------------------------------------------------------------
REM Helper: Terminate running instance to prevent file locks
REM ------------------------------------------------------------------------------
:kill_running_instance
tasklist /fi "imagename eq %BINARY_NAME%" 2>nul | find /i "%BINARY_NAME%" >nul
if %errorlevel% equ 0 (
    echo %YELLOW%[!]%NC% Terminating running instance of %BINARY_NAME%...
    taskkill /f /im "%BINARY_NAME%" >nul 2>nul
    timeout /t 1 /nobreak >nul
)
goto :eof

REM ------------------------------------------------------------------------------
REM Step 4: Shared Build & Validation Pipeline
REM ------------------------------------------------------------------------------
:run_build_pipeline

echo.
echo %BOLD%%CYAN%--- FORMATTING GO SOURCE ---%NC%
echo %CYAN%[+]%NC% Running gofmt on project files...

for %%F in (*.go) do (
    "%GOFMT_BIN%" -w "%%F"
    if errorlevel 1 (
        echo %RED%[X]%NC% gofmt error in %%F.
        pause
        exit /b 1
    )
)
echo %GREEN%[OK]%NC% Source code formatted successfully.

echo.
echo %BOLD%%CYAN%--- SYNCING GO DEPENDENCIES ---%NC%

set "BUILD_VENDOR_FLAG="
if exist "vendor" (
    echo %GREEN%[OK]%NC% Local vendor folder detected. Activating 100%% OFFLINE build mode.
    set "BUILD_VENDOR_FLAG=-mod=vendor"
) else (
    echo %CYAN%[+]%NC% Synchronizing module dependencies with go mod tidy...
    "%GO_BIN%" mod tidy >nul 2>nul
    if errorlevel 1 (
        echo %YELLOW%[!]%NC% Remote synchronization offline. Continuing with cached modules...
    ) else (
        echo %GREEN%[OK]%NC% Dependencies verified and synchronized.
    )
)

echo.
echo %BOLD%%CYAN%--- RUNNING STATIC ANALYSIS ---%NC%
echo %CYAN%[+]%NC% Running go vet...

if defined BUILD_VENDOR_FLAG (
    "%GO_BIN%" vet %BUILD_VENDOR_FLAG% ./...
) else (
    "%GO_BIN%" vet ./...
)

if errorlevel 1 (
    echo.
    echo %RED%[X]%NC% Static analysis detected errors.
    echo %YELLOW%[!]%NC% Please fix the issues identified above before compiling.
    echo.
    pause
    exit /b 1
)
echo %GREEN%[OK]%NC% Static analysis passed without issues.

echo.
echo %BOLD%%CYAN%--- RUNNING UNIT TESTS ---%NC%
if exist "*_test.go" (
    echo %CYAN%[+]%NC% Running test suites...
    if defined BUILD_VENDOR_FLAG (
        "%GO_BIN%" test %BUILD_VENDOR_FLAG% ./...
    ) else (
        "%GO_BIN%" test ./...
    )
    if errorlevel 1 (
        echo.
        echo %RED%[X]%NC% Test suites failed.
        echo.
        pause
        exit /b 1
    )
    echo %GREEN%[OK]%NC% All tests passed successfully.
) else (
    echo %YELLOW%[!]%NC% No unit test files found. Skipping testing phase.
)

echo.
echo %BOLD%%CYAN%--- COMPILING TARGET ENGINE: !TARGET_OS!/!TARGET_ARCH! ---%NC%
echo %CYAN%[+]%NC% Building %BINARY_NAME% (Optimized Release Build)...

set "GOOS=!TARGET_OS!"
set "GOARCH=!TARGET_ARCH!"
set "CGO_ENABLED=0"

if defined BUILD_VENDOR_FLAG (
    "%GO_BIN%" build %BUILD_VENDOR_FLAG% -trimpath -ldflags="-s -w" -o "%BINARY_NAME%" .
) else (
    "%GO_BIN%" build -trimpath -ldflags="-s -w" -o "%BINARY_NAME%" .
)

if errorlevel 1 (
    echo.
    echo %RED%[X]%NC% Compilation failed.
    echo %YELLOW%[!]%NC% Check the compiler output above for details.
    echo.
    pause
    exit /b 1
)

if not exist "%BINARY_NAME%" (
    echo.
    echo %RED%[X]%NC% Target executable %BINARY_NAME% was not generated.
    echo.
    pause
    exit /b 1
)

echo %GREEN%[OK]%NC% Build successful!
echo %GREEN%[OK]%NC% Target Binary: %CD%\%BINARY_NAME%

REM ------------------------------------------------------------------------------
REM Step 5: Execution / Cross-Compilation Handoff
REM ------------------------------------------------------------------------------
if "!TARGET_OS!"=="windows" (
    echo.
    echo %BOLD%%CYAN%--- LAUNCHING SWITCHER SUITE ---%NC%
    echo %DIM%[i] Elevating permissions to ensure system adapter access...%NC%
    echo.

    powershell -NoProfile -Command "Start-Sleep -Milliseconds 500" >nul 2>nul

    net session >nul 2>nul
    if %errorlevel% equ 0 (
        "%CD%\%BINARY_NAME%"
        set "APP_EXIT_CODE=%ERRORLEVEL%"
        goto :handle_exit
    ) else (
        powershell -NoProfile -ExecutionPolicy Bypass -Command ^
            "Start-Process -FilePath '%CD%\%BINARY_NAME%' -Verb RunAs -Wait"
        if %errorlevel% equ 0 (
            echo %GREEN%[OK]%NC% Session completed.
            exit /b 0
        ) else (
            echo.
            echo %RED%[!]%NC% Elevation prompt was cancelled or denied.
            pause
            exit /b 1
        )
    )
) else (
    echo.
    echo %BOLD%%GREEN%[OK] CROSS-COMPILATION COMPLETE!%NC%
    echo %YELLOW%[i] Standalone executable package generated for '!TARGET_OS!'.%NC%
    echo %YELLOW%[i] Ready to transfer and execute at: %CD%\%BINARY_NAME%%NC%
    echo.
    pause
    exit /b 0
)

:handle_exit
echo.
if "%APP_EXIT_CODE%"=="0" (
    echo %GREEN%[OK]%NC% Switcher Suite session terminated normally.
) else (
    echo %YELLOW%[!]%NC% Switcher Suite exited with code %APP_EXIT_CODE%.
)

echo.
pause
exit /b %APP_EXIT_CODE%