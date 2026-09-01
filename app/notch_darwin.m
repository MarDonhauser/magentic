#import <AppKit/AppKit.h>
#import <WebKit/WebKit.h>
#import "notch_darwin.h"

extern void magenticNotchResponse(char *payload);

static const CGFloat MgtNotchWidth = 420.0;
static const CGFloat MgtNotchHeight = 220.0;
static NSPanel *mgtNotchPanel = nil;
static WKWebView *mgtNotchWebView = nil;
static NSString *mgtPendingNotchEvent = nil;

@interface MgtNotchPanel : NSPanel
@end

@implementation MgtNotchPanel
- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

static void positionNotchPanel(void) {
    NSScreen *screen = [NSScreen screens].firstObject;
    if (screen == nil || mgtNotchPanel == nil) return;
    NSRect frame = screen.frame;
    CGFloat x = NSMidX(frame) - (MgtNotchWidth / 2.0);
    CGFloat y = NSMaxY(frame) - MgtNotchHeight;
    [mgtNotchPanel setFrameOrigin:NSMakePoint(MAX(NSMinX(frame), x), y)];
}

static void dispatchNotchJavaScript(NSString *eventName, NSString *json) {
    if (mgtNotchWebView == nil) return;
    NSString *script = [NSString stringWithFormat:
        @"window.__magenticNotchDispatch && window.__magenticNotchDispatch('%@', %@);",
        eventName, json ?: @"{}"];
    [mgtNotchWebView evaluateJavaScript:script completionHandler:nil];
}

@interface MgtNotchBridge : NSObject <WKScriptMessageHandler, WKNavigationDelegate>
@end

@implementation MgtNotchBridge
- (void)userContentController:(WKUserContentController *)controller
      didReceiveScriptMessage:(WKScriptMessage *)message {
    if ([message.name isEqualToString:@"notchInteractive"]) {
        BOOL interactive = [message.body isKindOfClass:[NSDictionary class]]
            && [message.body[@"interactive"] boolValue];
        mgtNotchPanel.ignoresMouseEvents = !interactive;
        if (interactive) {
            [mgtNotchPanel makeKeyWindow];
        }
        return;
    }
    if (![message.name isEqualToString:@"notchResponse"] ||
        ![NSJSONSerialization isValidJSONObject:message.body]) return;

	// The overlay owns the 900 ms resolved flash. Forget the native replay
	// payload here without dispatching notch://clear, which would cut it short.
	mgtPendingNotchEvent = nil;
    NSData *data = [NSJSONSerialization dataWithJSONObject:message.body options:0 error:nil];
    NSString *payload = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
    magenticNotchResponse((char *)payload.UTF8String);
}

- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    if (mgtPendingNotchEvent != nil) {
        dispatchNotchJavaScript(@"notch://event", mgtPendingNotchEvent);
    }
}
@end

void createNotchWindowC(const char *document) {
    NSString *html = document ? [NSString stringWithUTF8String:document] : @"";
    dispatch_async(dispatch_get_main_queue(), ^{
        if (mgtNotchPanel != nil) return;

        WKWebViewConfiguration *configuration = [[WKWebViewConfiguration alloc] init];
        MgtNotchBridge *bridge = [[MgtNotchBridge alloc] init];
        [configuration.userContentController addScriptMessageHandler:bridge name:@"notchResponse"];
        [configuration.userContentController addScriptMessageHandler:bridge name:@"notchInteractive"];

        mgtNotchWebView = [[WKWebView alloc]
            initWithFrame:NSMakeRect(0, 0, MgtNotchWidth, MgtNotchHeight)
            configuration:configuration];
        mgtNotchWebView.navigationDelegate = bridge;
        mgtNotchWebView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        [mgtNotchWebView setValue:@NO forKey:@"drawsBackground"];

        NSUInteger style = NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel;
        mgtNotchPanel = [[MgtNotchPanel alloc]
            initWithContentRect:NSMakeRect(0, 0, MgtNotchWidth, MgtNotchHeight)
            styleMask:style
            backing:NSBackingStoreBuffered
            defer:NO];
        mgtNotchPanel.title = @"Magentic Notch";
        mgtNotchPanel.backgroundColor = NSColor.clearColor;
        mgtNotchPanel.opaque = NO;
        mgtNotchPanel.hasShadow = NO;
        mgtNotchPanel.floatingPanel = YES;
        mgtNotchPanel.hidesOnDeactivate = NO;
        mgtNotchPanel.releasedWhenClosed = NO;
        mgtNotchPanel.ignoresMouseEvents = YES;
        mgtNotchPanel.level = NSStatusWindowLevel;
        mgtNotchPanel.collectionBehavior =
            NSWindowCollectionBehaviorCanJoinAllSpaces |
            NSWindowCollectionBehaviorStationary |
            NSWindowCollectionBehaviorFullScreenAuxiliary;
        mgtNotchPanel.contentView = mgtNotchWebView;
        positionNotchPanel();
        [mgtNotchWebView loadHTMLString:html baseURL:nil];
        [mgtNotchPanel orderFrontRegardless];
    });
}

void destroyNotchWindowC(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [mgtNotchPanel orderOut:nil];
        [mgtNotchPanel close];
        mgtNotchPanel = nil;
        mgtNotchWebView = nil;
        mgtPendingNotchEvent = nil;
    });
}

void showNotchEventC(const char *payload) {
    NSString *json = payload ? [NSString stringWithUTF8String:payload] : @"{}";
    dispatch_async(dispatch_get_main_queue(), ^{
        mgtPendingNotchEvent = [json copy];
        positionNotchPanel();
        [mgtNotchPanel orderFrontRegardless];
        dispatchNotchJavaScript(@"notch://event", mgtPendingNotchEvent);
    });
}

void clearNotchEventC(const char *identifier) {
    NSString *eventID = identifier ? [NSString stringWithUTF8String:identifier] : @"";
    dispatch_async(dispatch_get_main_queue(), ^{
        mgtPendingNotchEvent = nil;
        NSData *data = [NSJSONSerialization dataWithJSONObject:@{ @"id": eventID } options:0 error:nil];
        NSString *json = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
        dispatchNotchJavaScript(@"notch://clear", json);
    });
}

void acknowledgeNotchEventC(const char *identifier) {
    dispatch_async(dispatch_get_main_queue(), ^{
        mgtPendingNotchEvent = nil;
    });
}
