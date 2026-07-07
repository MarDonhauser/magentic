//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

static void setDockBadgeC(const char *label) {
	NSString *s = (label && label[0]) ? [NSString stringWithUTF8String:label] : nil;
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp dockTile].badgeLabel = s;
	});
}
*/
import "C"

import "unsafe"

func setDockBadge(label string) {
	cs := C.CString(label)
	C.setDockBadgeC(cs)
	C.free(unsafe.Pointer(cs))
}
