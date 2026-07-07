//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

@interface MgtNotifDelegate : NSObject <NSUserNotificationCenterDelegate>
@end
@implementation MgtNotifDelegate
- (BOOL)userNotificationCenter:(NSUserNotificationCenter *)center
     shouldPresentNotification:(NSUserNotification *)notification {
	return YES;
}
- (void)userNotificationCenter:(NSUserNotificationCenter *)center
       didActivateNotification:(NSUserNotification *)notification {
	[NSApp activateIgnoringOtherApps:YES];
}
@end

static id mgtNotifDelegate = nil;

static void initNotifierC() {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (mgtNotifDelegate == nil) {
			mgtNotifDelegate = [[MgtNotifDelegate alloc] init];
		}
		[NSUserNotificationCenter defaultUserNotificationCenter].delegate = mgtNotifDelegate;
	});
}

static void notifyC(const char *title, const char *message, const char *sound) {
	NSString *t = title ? [NSString stringWithUTF8String:title] : @"";
	NSString *m = message ? [NSString stringWithUTF8String:message] : @"";
	NSString *s = (sound && sound[0]) ? [NSString stringWithUTF8String:sound] : nil;
	dispatch_async(dispatch_get_main_queue(), ^{
		NSUserNotification *n = [[NSUserNotification alloc] init];
		n.title = t;
		n.informativeText = m;
		if (s) n.soundName = s;
		[[NSUserNotificationCenter defaultUserNotificationCenter] deliverNotification:n];
	});
}

#pragma clang diagnostic pop
*/
import "C"

import (
	"unsafe"

	"magentic/core"
)

func nativeNotify(title, message, sound string) {
	ct, cm, cs := C.CString(title), C.CString(message), C.CString(sound)
	C.notifyC(ct, cm, cs)
	C.free(unsafe.Pointer(ct))
	C.free(unsafe.Pointer(cm))
	C.free(unsafe.Pointer(cs))
}

func installNativeNotifier() {
	C.initNotifierC()
	core.Notifier = nativeNotify
}
