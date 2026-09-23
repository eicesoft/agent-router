# 编译 macOS 与 Windows 可执行文件。
# 依赖：wails CLI、Node.js（前端构建）；Windows 交叉编译需要 mingw-w64。
#
# 用法：
#   make            # 编译 mac + windows
#   make mac        # 只编译 mac（darwin/arm64，产物 build/bin/agent-router.app）
#   make windows    # 只编译 windows（产物 build/bin/agent-router.exe）
#   make installer  # 打 Windows NSIS 安装包（产物 build/bin/*-setup.exe，需 brew install nsis）
#   make clean      # 清理产物
# 可用 MAC_PLATFORM=darwin/universal、WIN_PLATFORM=windows/386 等覆盖目标平台。

MAC_PLATFORM ?= darwin/arm64
WIN_PLATFORM ?= windows/amd64

.PHONY: all mac windows installer clean

all: mac windows

mac:
	wails build -platform $(MAC_PLATFORM)

windows:
	wails build -platform $(WIN_PLATFORM)

installer:
	wails build -platform $(WIN_PLATFORM) -nsis

clean:
	rm -rf build/bin
