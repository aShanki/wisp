#import <AppKit/AppKit.h>
#import <UserNotifications/UserNotifications.h>

#include "rpcclient.h"
#include <QEvent>
#include <QGuiApplication>
#include <QJsonObject>
#include <QQuickWindow>

@interface WispDesktop : NSObject <UNUserNotificationCenterDelegate>
@property(nonatomic, strong) NSStatusItem *status;
@property(nonatomic, assign) QQuickWindow *window;
@property(nonatomic, assign) RpcClient *rpc;
@property(nonatomic, strong) NSMutableDictionary<NSString *, NSNumber *> *unread;
- (void)show;
- (void)quit;
- (void)notification:(NSNotification *)notification;
@end

@implementation WispDesktop
- (void)show {
    QMetaObject::invokeMethod(self.window, "reveal");
    [NSApp activateIgnoringOtherApps:YES];
}
- (void)quit {
    if (!self.rpc->isConnected()) { QCoreApplication::quit(); return; }
    self.rpc->call(QStringLiteral("daemon.shutdown"), QJsonObject{}, [](const QJsonValue &, const QJsonObject &) {
        QCoreApplication::quit();
    });
}
- (void)notification:(NSNotification *)notification {
    NSDictionary *data = [NSJSONSerialization JSONObjectWithData:
        [notification.object dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
    if (!data) return;
    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    NSString *action = data[@"action"];
    if ([action isEqual:@"clearAll"]) {
        [center removeAllDeliveredNotifications];
        [center removeAllPendingNotificationRequests];
    } else if ([action isEqual:@"clear"]) {
        [center removeDeliveredNotificationsWithIdentifiers:@[data[@"id"]]];
        [center removePendingNotificationRequestsWithIdentifiers:@[data[@"id"]]];
    } else if ([action isEqual:@"show"]) {
        UNMutableNotificationContent *content = [UNMutableNotificationContent new];
        content.title = data[@"title"] ?: @"Wisp";
        content.body = data[@"body"] ?: @"";
        content.userInfo = data;
        content.categoryIdentifier = data[@"call"] ? @"call" : @"message";
        content.sound = [UNNotificationSound soundNamed:@"notification.caf"];
        [center addNotificationRequest:[UNNotificationRequest requestWithIdentifier:data[@"id"]
            content:content trigger:nil] withCompletionHandler:^(NSError *error) {
                if (error) NSLog(@"Wisp notification: %@", error.localizedDescription);
            }];
    }
}
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
      willPresentNotification:(UNNotification *)notification
        withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completion {
    completion(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionSound);
}
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
 didReceiveNotificationResponse:(UNNotificationResponse *)response
         withCompletionHandler:(void (^)(void))completion {
    NSDictionary *data = response.notification.request.content.userInfo;
    dispatch_async(dispatch_get_main_queue(), ^{
    QString chat = QString::fromNSString(data[@"chat"] ?: @"");
    QString action = QString::fromNSString(response.actionIdentifier);
    if (action == "reply") {
        auto *reply = (UNTextInputNotificationResponse *)response;
        self.rpc->call(QStringLiteral("messages.send"), QJsonObject{
            {"chat", chat}, {"text", QString::fromNSString(reply.userText)},
            {"replyTo", QString::fromNSString(data[@"message"] ?: @"")}});
    } else if (action == "read") {
        self.rpc->call(QStringLiteral("chats.markRead"), QJsonObject{{"chat", chat}});
    } else if (action == "decline") {
        self.rpc->call(QStringLiteral("calls.reject"), QJsonObject{{"callId", QString::fromNSString(data[@"call"])}});
    } else {
        emit self.rpc->openChatRequested(chat);
        [self show];
    }
    completion();
    });
}
@end

class MacWindowEvents : public QObject {
public:
    explicit MacWindowEvents(QQuickWindow *window) : QObject(window), m_window(window) {}
    bool eventFilter(QObject *watched, QEvent *event) override {
        if (watched == qApp && event->type() == QEvent::ApplicationActivate
            && (!m_window->isVisible() || m_window->visibility() == QWindow::Minimized))
            QMetaObject::invokeMethod(m_window, "reveal");
        return false;
    }
private:
    QQuickWindow *m_window;
};

void setupMacDesktop(QQuickWindow *window, RpcClient *rpc) {
    QGuiApplication::setQuitOnLastWindowClosed(false);
    auto *filter = new MacWindowEvents(window);
    qApp->installEventFilter(filter);
    static WispDesktop *desktop = [WispDesktop new];
    desktop.window = window;
    desktop.rpc = rpc;
    desktop.unread = [NSMutableDictionary new];
    desktop.status = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
    NSImage *icon = [[NSImage imageNamed:NSImageNameApplicationIcon] copy];
    icon.size = NSMakeSize(18, 18);
    desktop.status.button.image = icon;
    desktop.status.button.toolTip = @"Wisp";
    NSMenu *menu = [NSMenu new];
    NSMenuItem *open = [menu addItemWithTitle:@"Open Wisp" action:@selector(show) keyEquivalent:@""];
    open.target = desktop;
    [menu addItem:[NSMenuItem separatorItem]];
    NSMenuItem *quit = [menu addItemWithTitle:@"Quit Wisp" action:@selector(quit) keyEquivalent:@"q"];
    quit.target = desktop;
    desktop.status.menu = menu;
    [[NSDistributedNotificationCenter defaultCenter] addObserver:desktop selector:@selector(notification:)
        name:@"io.github.aShanki.wisp.notification" object:nil];
    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    center.delegate = desktop;
    UNNotificationAction *read = [UNNotificationAction actionWithIdentifier:@"read" title:@"Mark as read" options:0];
    UNNotificationAction *reply = [UNTextInputNotificationAction actionWithIdentifier:@"reply" title:@"Reply"
        options:0 textInputButtonTitle:@"Send" textInputPlaceholder:@"Message"];
    UNNotificationCategory *message = [UNNotificationCategory categoryWithIdentifier:@"message"
        actions:@[reply, read] intentIdentifiers:@[] options:0];
    UNNotificationAction *decline = [UNNotificationAction actionWithIdentifier:@"decline" title:@"Decline" options:0];
    UNNotificationCategory *call = [UNNotificationCategory categoryWithIdentifier:@"call"
        actions:@[decline] intentIdentifiers:@[] options:0];
    [center setNotificationCategories:[NSSet setWithObjects:message, call, nil]];
    [center requestAuthorizationWithOptions:UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge
        completionHandler:^(BOOL, NSError *) {}];
    QObject::connect(rpc, &RpcClient::event, window, [](const QString &event, const QJsonObject &params) {
        if (event == "chat.update") {
            QJsonObject chat = params.value("chat").toObject();
            desktop.unread[chat.value("jid").toString().toNSString()] = @(chat.value("muted").toBool() ? 0 : chat.value("unread").toInt());
        } else if (event == "auth.state" && params.value("state").toString() != "paired") {
            [desktop.unread removeAllObjects];
        }
        int unread = 0;
        for (NSNumber *count in desktop.unread.allValues) unread += count.intValue;
        NSApp.dockTile.badgeLabel = unread ? [NSString stringWithFormat:@"%d", unread] : nil;
    });
}
