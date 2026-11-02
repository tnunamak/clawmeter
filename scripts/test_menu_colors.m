// Exercise the actual AppKit adapter without opening a menu or status item.
#import "../internal/systray/systray_darwin.m"

void systray_ready(void) {}
void systray_on_exit(void) {}
void systray_left_click(void) {}
void systray_right_click(void) {}
void systray_menu_item_selected(int menu_id) {}
void systray_menu_will_open(void) {}

static void require(bool condition, NSString *message) {
  if (!condition) { fprintf(stderr, "%s\n", message.UTF8String); exit(1); }
}

int main(void) {
  @autoreleasepool {
    [NSApplication sharedApplication];
    NSString *plain = @"📌 é 账户 █┃░▒";
    NSString *json = @"[{\"text\":\"📌 é 账户 \",\"style\":0},{\"text\":\"█\",\"style\":1},{\"text\":\"┃\",\"style\":4},{\"text\":\"░\",\"style\":2},{\"text\":\"▒\",\"style\":3}]";
    NSAttributedString *title = styledMenuTitle(json, plain);
    require([title.string isEqualToString:plain], @"Unicode title changed");
    id red = [title attribute:NSForegroundColorAttributeName atIndex:[plain rangeOfString:@"█"].location effectiveRange:NULL];
    id green = [title attribute:NSForegroundColorAttributeName atIndex:[plain rangeOfString:@"░"].location effectiveRange:NULL];
    NSFont *pace = [title attribute:NSFontAttributeName atIndex:[plain rangeOfString:@"┃"].location effectiveRange:NULL];
    require([red isEqual:[NSColor systemRedColor]], @"ahead color missing");
    require([green isEqual:[NSColor systemGreenColor]], @"behind color missing");
    id muted = [title attribute:NSForegroundColorAttributeName atIndex:[plain rangeOfString:@"▒"].location effectiveRange:NULL];
    require([muted isEqual:[NSColor secondaryLabelColor]], @"future cell muting missing");
    require([title attribute:NSForegroundColorAttributeName atIndex:0 effectiveRange:NULL] == nil, @"ordinary title text was colored");
    require(([[NSFontManager sharedFontManager] traitsOfFont:pace] & NSBoldFontMask) != 0, @"pace emphasis missing");
    for (NSString *invalid in @[@"", @"null", @"[]", @"{}", @"[null]", @"[{\"text\":\"x\",\"style\":true}]", @"[{\"text\":\"x\",\"style\":-1}]", @"[{\"text\":\"x\",\"style\":1.5}]", @"[{\"text\":\"x\",\"style\":256}]", @"[{\"text\":\"x\",\"style\":\"red\"}]", @"[{\"text\":7,\"style\":0}]"]) {
      require(styledMenuTitle(invalid, @"x") == nil, @"invalid spans accepted");
    }
    require(styledMenuTitle(json, @"different title") == nil, @"mismatched spans accepted");

    // KVC initializes just the adapter's in-memory menu, avoiding a status item.
    owner = [[SystrayAppDelegate alloc] init];
    NSMenu *menu = [[NSMenu alloc] initWithTitle:@"fixture"];
    [owner setValue:menu forKey:@"menu"];
    MenuItem *row = [[MenuItem alloc] initWithId:7 withParentMenuId:0 withTitle:plain.UTF8String withTooltip:"fixture" withTitleSpans:json.UTF8String withDisabled:0 withChecked:1];
    [owner add_or_update_menu_item:row];
    NSMenuItem *item = [menu itemWithTag:7];
    require([item.attributedTitle.string isEqualToString:plain] && item.enabled, @"styled native item missing");
    require(item.target == owner && [item.representedObject intValue] == 7 && item.state == NSControlStateValueOn, @"menu action or check changed");
    row->disabled = 1; [owner add_or_update_menu_item:row];
    require(!item.enabled && item.attributedTitle.length == 0, @"disabled item retained colors");
    row->disabled = 0; [owner add_or_update_menu_item:row];
    require(item.enabled && [item.attributedTitle.string isEqualToString:plain], @"reenable lost colors");
    row->titleSpansJSON = nil; [owner add_or_update_menu_item:row];
    require(item.attributedTitle.length == 0 && [item.title isEqualToString:plain], @"plain update left colors or lost text");
    puts("PASS native attributed menu colors, Unicode, invalid input, disabled/reenabled/plain updates and action identity");
  }
  return 0;
}
