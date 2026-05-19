@echo off
chcp 65001 >nul

REM 切换到脚本所在目录
cd /d "%~dp0"

echo ========================================
echo   资产信息管理系统 - 环境设置脚本
echo ========================================
echo.
echo 工作目录: %cd%
echo.

REM 检查Go是否已安装
where go >nul 2>&1
if %errorlevel% == 0 (
    echo [OK] Go 已安装
    go version
    echo.
    goto :install_deps
) else (
    echo [!] 未检测到 Go,正在尝试安装...
    echo.
)

REM 尝试使用winget安装Go
echo 正在使用 winget 安装 Go...
winget install GoLang.Go --silent --accept-package-agreements --accept-source-agreements
if %errorlevel% == 0 (
    echo [OK] Go 安装成功
    echo 请重新打开此脚本以继续
    pause
    exit /b
) else (
    echo [ERROR] winget 安装失败
    echo.
    echo 请手动安装 Go:
    echo 1. 访问 https://golang.org/dl/
    echo 2. 下载 Windows 安装包
    echo 3. 运行安装程序
    echo 4. 重新打开此脚本
    echo.
    pause
    exit /b
)

:install_deps
echo ========================================
echo   安装依赖包
echo ========================================
echo.

go mod tidy
if %errorlevel% == 0 (
    echo [OK] 依赖安装成功
    echo.
) else (
    echo [ERROR] 依赖安装失败
    echo 请检查网络连接
    pause
    exit /b
)

echo ========================================
echo   编译项目
echo ========================================
echo.

go build -o asset-manager.exe
if %errorlevel% == 0 (
    echo [OK] 编译成功
    echo.

    REM 检查端口占用
    set PORT=8082
    netstat -ano | findstr ":%PORT%" | findstr "LISTENING" >nul 2>&1
    if %errorlevel% == 0 (
        echo [!] 端口 %PORT% 已被占用，正在尝试停止旧进程...
        for /f "tokens=5" %%a in ('netstat -ano ^| findstr ":%PORT%" ^| findstr "LISTENING"') do (
            taskkill /F /PID %%a >nul 2>&1
        )
        timeout /t 1 >nul
    )

    echo 正在启动系统...
    echo 浏览器访问: http://localhost:%PORT%
    echo 默认账号: admin / admin123
    echo.
    echo 按 Ctrl+C 停止服务
    echo.

    set PORT=%PORT%
    asset-manager.exe
) else (
    echo [ERROR] 编译失败
    echo 请检查代码错误
    pause
    exit /b
)
