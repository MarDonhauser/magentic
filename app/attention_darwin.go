//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

static NSInteger mgtAttentionRequest = 0;

// NSCriticalRequest lässt das Dock-Icon hüpfen, bis die App aktiviert wird —
// eine Benachrichtigung allein verschwindet nach ein paar Sekunden ungesehen.
static void requestAttentionC(int critical) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (mgtAttentionRequest != 0) {
			[NSApp cancelUserAttentionRequest:mgtAttentionRequest];
		}
		mgtAttentionRequest = [NSApp requestUserAttention:
			(critical ? NSCriticalRequest : NSInformationalRequest)];
	});
}

static void cancelAttentionC() {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (mgtAttentionRequest != 0) {
			[NSApp cancelUserAttentionRequest:mgtAttentionRequest];
			mgtAttentionRequest = 0;
		}
	});
}

static void bringToFrontC() {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp activateIgnoringOtherApps:YES];
	});
}
*/
import "C"

func requestAttention(critical bool) {
	c := C.int(0)
	if critical {
		c = 1
	}
	C.requestAttentionC(c)
}

func cancelAttention() {
	C.cancelAttentionC()
}

func bringToFront() {
	C.bringToFrontC()
}
