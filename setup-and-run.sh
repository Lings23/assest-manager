#!/bin/bash

# 切换到脚本所在目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "========================================"
echo "  资产信息管理系统 - 环境设置脚本"
echo "========================================"
echo ""
echo "工作目录: $SCRIPT_DIR"
echo ""

# 检查Go是否已安装
if command -v go &> /dev/null; then
    echo "[OK] Go 已安装"
    go version
    echo ""
else
    echo "[!] 未检测到 Go,正在尝试安装..."
    echo ""

    # 检测操作系统
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$NAME
    else
        OS="unknown"
    fi

    case "$OS" in
        *Ubuntu*|*Debian*)
            echo "检测到 Debian/Ubuntu 系统"
            sudo apt update
            sudo apt install -y golang-go
            ;;
        *CentOS*|*RHEL*|*Fedora*)
            echo "检测到 RHEL/CentOS/Fedora 系统"
            sudo yum install -y golang
            ;;
        *)
            echo "请手动安装 Go:"
            echo "1. 访问 https://golang.org/dl/"
            echo "2. 下载 Linux 安装包"
            echo "3. 解压到 /usr/local"
            echo "4. 添加 PATH: export PATH=\$PATH:/usr/local/go/bin"
            echo "5. 重新运行此脚本"
            exit 1
            ;;
    esac

    # 验证安装
    if command -v go &> /dev/null; then
        echo "[OK] Go 安装成功"
        go version
    else
        echo "[ERROR] Go 安装失败"
        exit 1
    fi
fi

echo "========================================"
echo "  安装依赖包"
echo "========================================"
echo ""

go mod tidy
if [ $? -eq 0 ]; then
    echo "[OK] 依赖安装成功"
    echo ""
else
    echo "[ERROR] 依赖安装失败"
    echo "请检查网络连接"
    exit 1
fi

echo "========================================"
echo "  编译项目"
echo "========================================"
echo ""

go build -o asset-manager
if [ $? -eq 0 ]; then
    echo "[OK] 编译成功"
    echo ""

    # 检查是否有进程占用端口
    PORT=8082
    if lsof -Pi :$PORT -sTCP:LISTEN -t >/dev/null 2>&1; then
        echo "[!] 端口 $PORT 已被占用，正在尝试停止旧进程..."
        lsof -Pi :$PORT -sTCP:LISTEN -t | xargs -r kill -9 2>/dev/null
        sleep 1
    fi

    echo "正在启动系统..."
    echo "浏览器访问: http://localhost:$PORT"
    echo "默认账号: admin / admin123"
    echo ""
    echo "按 Ctrl+C 停止服务"
    echo ""

    export PORT=$PORT
    ./asset-manager
else
    echo "[ERROR] 编译失败"
    echo "请检查代码错误"
    exit 1
fi
