#import <Foundation/Foundation.h>

void wispNotify(const char *payload) {
    @autoreleasepool {
        NSString *json = [NSString stringWithUTF8String:payload];
        [[NSDistributedNotificationCenter defaultCenter]
            postNotificationName:@"io.github.aShanki.wisp.notification"
            object:json userInfo:nil deliverImmediately:YES];
    }
}
