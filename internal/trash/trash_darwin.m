//go:build darwin && cgo

#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// synckeeperMoveToTrash moves the item at path into the user's Trash via
// NSFileManager, which records the original location so Finder's "Put Back"
// works and handles per-volume .Trashes itself.
//
// Returns 1 on success. On failure returns 0 and, when errMsg is non-NULL,
// stores a malloc'd copy of the localized error description there; the Go
// caller frees it.
int synckeeperMoveToTrash(const char *path, char **errMsg) {
    @autoreleasepool {
        NSString *p = [NSString stringWithUTF8String:path];
        if (p == nil) {
            if (errMsg != NULL) *errMsg = strdup("path is not valid UTF-8");
            return 0;
        }
        NSURL *url = [NSURL fileURLWithPath:p];
        NSError *err = nil;
        BOOL ok = [[NSFileManager defaultManager] trashItemAtURL:url
                                                resultingItemURL:nil
                                                           error:&err];
        if (!ok) {
            if (errMsg != NULL) {
                const char *desc = [[err localizedDescription] UTF8String];
                *errMsg = strdup(desc != NULL ? desc : "trashItemAtURL failed");
            }
            return 0;
        }
        return 1;
    }
}
