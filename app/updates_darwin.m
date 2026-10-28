#import <AppKit/AppKit.h>
#include <string.h>
#import "updates_darwin.h"

// Sparkle is embedded by the release build and loaded from this app only.
// Keeping the small ABI here lets `go test` and Wails development builds run
// without downloading or linking the distribution framework.
@protocol MgtSparkleUpdater <NSObject>
@property (nonatomic, readonly) BOOL canCheckForUpdates;
@property (nonatomic, readonly) BOOL sessionInProgress;
- (BOOL)startUpdater:(NSError **)error;
@end

@protocol MgtSparkleController <NSObject>
- (instancetype)initWithStartingUpdater:(BOOL)startUpdater updaterDelegate:(id)delegate userDriverDelegate:(id)driverDelegate;
@property (nonatomic, readonly) id<MgtSparkleUpdater> updater;
- (void)checkForUpdates:(id)sender;
@end

static id<MgtSparkleController> mgtUpdater = nil;
static NSString *mgtUpdaterError = nil;

static void onUpdateMain(void (^block)(void)) {
    if ([NSThread isMainThread]) block();
    else dispatch_sync(dispatch_get_main_queue(), block);
}

static NSString *updaterConfigurationError(void) {
    NSBundle *bundle = [NSBundle mainBundle];
    if (![bundle.bundlePath.pathExtension isEqualToString:@"app"] ||
        ![[bundle objectForInfoDictionaryKey:@"CFBundleIdentifier"] isEqualToString:@"com.wails.magentic"]) {
        return @"Dieser Entwicklungsbuild unterstützt keine App-Updates. Installiere eine veröffentlichte Version von magentic.";
    }
    NSString *key = [bundle objectForInfoDictionaryKey:@"SUPublicEDKey"];
    NSData *keyData = [key isKindOfClass:[NSString class]] ? [[NSData alloc] initWithBase64EncodedString:key options:0] : nil;
    NSString *feed = [bundle objectForInfoDictionaryKey:@"SUFeedURL"];
    NSURL *url = [feed isKindOfClass:[NSString class]] ? [NSURL URLWithString:feed] : nil;
    if (keyData.length != 32 || ![url.scheme isEqualToString:@"https"] || url.host.length == 0 ||
        ![[bundle objectForInfoDictionaryKey:@"SUVerifyUpdateBeforeExtraction"] boolValue] ||
        ![[bundle objectForInfoDictionaryKey:@"SURequireSignedFeed"] boolValue]) {
        return @"App-Updates sind in diesem Build noch nicht eingerichtet. Installiere eine veröffentlichte Version von magentic.";
    }
    NSString *path = [bundle.privateFrameworksPath stringByAppendingPathComponent:@"Sparkle.framework"];
    if (![[NSFileManager defaultManager] fileExistsAtPath:path]) {
        return @"Die Update-Komponente fehlt. Installiere magentic erneut aus der veröffentlichten Version.";
    }
    return mgtUpdaterError;
}

static NSString *startUpdater(void) {
    NSString *problem = updaterConfigurationError();
    if (problem != nil || mgtUpdater != nil) return problem;

    NSBundle *framework = [NSBundle bundleWithPath:[[NSBundle mainBundle].privateFrameworksPath
        stringByAppendingPathComponent:@"Sparkle.framework"]];
    NSError *error = nil;
    if (![framework loadAndReturnError:&error]) {
        return mgtUpdaterError = @"Die Update-Komponente konnte nicht geladen werden. Installiere magentic erneut.";
    }
    Class controllerClass = [framework classNamed:@"SPUStandardUpdaterController"];
    if (controllerClass == Nil) {
        return mgtUpdaterError = @"Die Update-Komponente ist unvollständig. Installiere magentic erneut.";
    }
    mgtUpdater = [(id<MgtSparkleController>)[controllerClass alloc]
        initWithStartingUpdater:NO updaterDelegate:nil userDriverDelegate:nil];
    if (mgtUpdater == nil || ![mgtUpdater.updater startUpdater:&error]) {
        mgtUpdater = nil;
        return mgtUpdaterError = @"Die Update-Komponente konnte nicht gestartet werden. Installiere magentic erneut.";
    }
    return nil;
}

char *updatesStatusC(void) {
    __block char *result = NULL;
    onUpdateMain(^{
        @autoreleasepool {
            NSString *version = [[NSBundle mainBundle] objectForInfoDictionaryKey:@"CFBundleShortVersionString"];
            NSString *reason = updaterConfigurationError();
            NSDictionary *status = @{
                @"version": version.length > 0 ? version : @"Entwicklungsbuild",
                @"enabled": [NSNumber numberWithBool:reason == nil],
                @"canCheck": [NSNumber numberWithBool:reason == nil && (mgtUpdater == nil || mgtUpdater.updater.canCheckForUpdates)],
                @"sessionInProgress": [NSNumber numberWithBool:mgtUpdater != nil && mgtUpdater.updater.sessionInProgress],
                @"reason": reason ?: @""
            };
            NSData *data = [NSJSONSerialization dataWithJSONObject:status options:0 error:nil];
            result = strdup([[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding].UTF8String);
        }
    });
    return result;
}

char *checkForUpdatesC(void) {
    __block char *result = NULL;
    onUpdateMain(^{
        @autoreleasepool {
            NSString *problem = startUpdater();
            if (problem == nil && !mgtUpdater.updater.canCheckForUpdates) {
                problem = @"Ein Update wird bereits vorbereitet. Bitte warte einen Moment und versuche es erneut.";
            }
            if (problem == nil) {
                [NSApp activateIgnoringOtherApps:YES];
                [mgtUpdater checkForUpdates:nil];
            }
            result = strdup((problem ?: @"").UTF8String);
        }
    });
    return result;
}

@interface MgtUpdateMenuTarget : NSObject <NSMenuItemValidation>
@end

@implementation MgtUpdateMenuTarget
- (void)checkForUpdates:(id)sender {
    char *problem = checkForUpdatesC();
    if (problem[0] != '\0') {
        NSAlert *alert = [[NSAlert alloc] init];
        alert.messageText = @"Update nicht möglich";
        alert.informativeText = [NSString stringWithUTF8String:problem];
        [alert addButtonWithTitle:@"OK"];
        [alert runModal];
    }
    free(problem);
}
- (BOOL)validateMenuItem:(NSMenuItem *)item {
    return updaterConfigurationError() == nil && (mgtUpdater == nil || mgtUpdater.updater.canCheckForUpdates);
}
@end

void installUpdaterMenuC(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        static MgtUpdateMenuTarget *target = nil;
        if (target != nil) return;
        if (NSApp.mainMenu.numberOfItems == 0) return;
        NSMenu *menu = [NSApp.mainMenu itemAtIndex:0].submenu;
        if (menu == nil) return;
        target = [[MgtUpdateMenuTarget alloc] init];
        NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:@"Nach Updates suchen…"
            action:@selector(checkForUpdates:) keyEquivalent:@""];
        item.target = target;
        [menu insertItem:item atIndex:MIN(2, menu.numberOfItems)];
    });
}
