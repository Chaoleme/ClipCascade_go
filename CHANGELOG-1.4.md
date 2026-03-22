# ClipCascade v1.4 - macOS 剪贴板监控性能修复

## 发布说明

本次更新专注于修复 macOS 桌面端长期存在的后台 CPU 占用过高问题。  
在修复前，即使不做任何操作，仅运行 ClipCascade 便可导致系统 CPU 接近 100%。

## 问题根因

`client-desktop/clipboard/monitor_darwin.go` 中的 `getPlatformFilePaths()` 函数  
原先通过启动 `osascript` 子进程来检测剪贴板中的文件路径。  
该函数由 `watchEventDriven()` 内的 `fileTicker` **每秒调用一次**，  
持续触发 macOS 系统权限守护进程 `tccd` 的审查，造成严重的连锁 CPU 开销：

| 进程 | 修复前 CPU 占用 |
|---|---|
| `osascript` | ~14% |
| `tccd` (权限检查) | ~53% |
| `WindowServer` | ~36% |
| `launchservicesd` | ~30% |

## 修复内容

### [fix] `client-desktop/clipboard/monitor_darwin.go` — 全部替换为 CGO 原生调用

将三个函数的 `osascript` 子进程实现，全部替换为通过 CGO 直接调用 `NSPasteboard` 原生 API：

| 函数 | 修复前 | 修复后 |
|---|---|---|
| `getPlatformChangeCount()` | `osascript` 子进程 | `[NSPasteboard generalPasteboard].changeCount` |
| `getPlatformFilePaths()` | `osascript` 子进程 | `NSPasteboard readObjectsForClasses:NSURL` |
| `setPlatformFilePaths()` | `osascript` 子进程 | `NSPasteboard writeObjects:@[NSURL]` |

- 同时移除了已废弃的 `NSFilenamesPboardType`，统一使用现代 `NSPasteboardTypeFileURL` API（macOS 10.14+）。
- 文件共享功能逻辑与轮询间隔（1秒）保持不变，仅"如何访问剪贴板"的底层实现发生了变化。

## 影响范围

- **macOS 桌面端**：✅ 修复，CPU 恢复正常
- **Windows 桌面端**：无变动（原已使用 Win32 syscall，无此问题）
- **Linux 桌面端**：无变动（使用 `xclip`，影响较小）
- **iOS / Android 移动端**：无变动（无本地剪贴板轮询逻辑，无此问题）

## 编译验证

```bash
cd client-desktop
go build ./clipboard/...   # 零警告、零错误
go test ./clipboard/... -v # 全部 PASS
```

---

*记录人：Antigravity AI*
