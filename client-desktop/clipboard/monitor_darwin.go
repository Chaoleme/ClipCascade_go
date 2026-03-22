//go:build darwin

package clipboard

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>

// getChangeCount 返回 NSPasteboard 的 changeCount，用于检测剪贴板是否发生变化。
// 每次剪贴板内容更改时，changeCount 会递增。调用无副作用，耗时 <1µs。
long long getChangeCount() {
    return (long long)[[NSPasteboard generalPasteboard] changeCount];
}

// getFilePaths 返回剪贴板中的文件路径列表（换行符分隔）。
// 使用现代 NSPasteboardTypeFileURL API（macOS 10.14+），不使用已废弃的 NSFilenamesPboardType。
// 如果剪贴板中没有文件，返回空字符串。
const char* getFilePaths() {
    NSPasteboard *pb = [NSPasteboard generalPasteboard];

    // 读取 public.file-url（现代标准格式，支持 Finder 拖拽、沙盒 app 等所有场景）
    NSArray *classes = @[[NSURL class]];
    NSDictionary *opts = @{NSPasteboardURLReadingFileURLsOnlyKey: @YES};
    NSArray *urls = [pb readObjectsForClasses:classes options:opts];
    if ([urls isKindOfClass:[NSArray class]] && urls.count > 0) {
        NSMutableArray *paths = [NSMutableArray arrayWithCapacity:urls.count];
        for (NSURL *url in urls) {
            if (url.path) {
                [paths addObject:url.path];
            }
        }
        if (paths.count > 0) {
            NSString *joined = [paths componentsJoinedByString:@"\n"];
            return strdup([joined UTF8String]);
        }
    }

    return strdup("");
}


// setFilePath 将单个文件路径写入 macOS 剪贴板（file URL 格式）。
void setFilePath(const char* path) {
    NSString *nsPath = [NSString stringWithUTF8String:path];
    NSURL *url = [NSURL fileURLWithPath:nsPath];
    if (!url) return;
    NSPasteboard *pb = [NSPasteboard generalPasteboard];
    [pb clearContents];
    [pb writeObjects:@[url]];
}
*/
import "C"
import (
	"log/slog"
	"os"
	"strings"
	"unsafe"
)

// getPlatformChangeCount 通过 NSPasteboard.changeCount 检测剪贴板变化。
// 直接调用原生 API，无进程开销，耗时 <1µs。
func getPlatformChangeCount() int64 {
	return int64(C.getChangeCount())
}

// getPlatformFilePaths 通过 NSPasteboard 原生 API 获取剪贴板中的文件路径列表。
// 完全替代原有的 osascript 子进程方案，消除每次调用产生新进程的 CPU 开销。
func getPlatformFilePaths() ([]string, error) {
	cStr := C.getFilePaths()
	defer C.free(unsafe.Pointer(cStr))

	raw := strings.TrimSpace(C.GoString(cStr))
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, "\n")
	var validPaths []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// 严格磁盘校验，确保路径真实存在
		if _, err := os.Stat(p); err == nil {
			validPaths = append(validPaths, p)
		}
	}
	return validPaths, nil
}

// setPlatformFilePaths 通过 NSPasteboard 原生 API 将文件路径写入 macOS 剪贴板。
// 替代原有的 osascript 子进程方案。目前仅支持写入第一个路径（与原逻辑一致）。
func setPlatformFilePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	slog.Info("剪贴板：通过 NSPasteboard 将文件路径写入 macOS 剪贴板...", "路径", paths[0])
	cPath := C.CString(paths[0])
	defer C.free(unsafe.Pointer(cPath))
	C.setFilePath(cPath)
	return nil
}
