// Shuttle Server menu bar icon. Run by Shuttle Server itself with
//   osascript -l JavaScript menubar.js <status file> <server pid>
// It only reads the status file and opens the page. It quits by itself when
// the status file stops being updated, so it never outlives the server.
ObjC.import('Cocoa');

function run(argv) {
  const statusPath = argv[0], serverPid = String(argv[1]);
  const app = $.NSApplication.sharedApplication;
  app.setActivationPolicy($.NSApplicationActivationPolicyAccessory); // no Dock icon

  const item = $.NSStatusBar.systemStatusBar.statusItemWithLength($.NSVariableStatusItemLength);
  const menu = $.NSMenu.alloc.init;
  menu.autoenablesItems = false;
  let pageURL = 'http://localhost:8470';
  let lastGood = Date.now(), lastLevel = '';

  ObjC.registerSubclass({
    name: 'ShuttleMenuTarget',
    methods: {
      'tick:': { types: ['void', ['id']], implementation: function () { refresh(); } },
      'openPage:': { types: ['void', ['id']], implementation: function () {
        $.NSWorkspace.sharedWorkspace.openURL($.NSURL.URLWithString(pageURL));
      } },
      'quitServer:': { types: ['void', ['id']], implementation: function () {
        $.NSTask.launchedTaskWithLaunchPathArguments('/bin/kill', $(['-INT', serverPid]));
      } },
    },
  });
  const target = $.ShuttleMenuTarget.alloc.init;

  function add(title, action) {
    const mi = $.NSMenuItem.alloc.initWithTitleActionKeyEquivalent(title, action || '', '');
    if (action) { mi.target = target; } else { mi.enabled = false; }
    menu.addItem(mi);
    return mi;
  }
  const head = add('Shuttle Server — starting', null);
  const line = add('', null);
  menu.addItem($.NSMenuItem.separatorItem);
  add('Open Shuttle page', 'openPage:');
  add('Quit Shuttle Server', 'quitServer:');
  item.menu = menu;

  const icons = {
    ok:   ['arrow.up.arrow.down.circle', '⇅'],
    warn: ['exclamationmark.triangle', '⚠︎'],
    bad:  ['xmark.octagon', '✕'],
  };
  function setIcon(level) {
    if (level === lastLevel) return;
    lastLevel = level;
    const pick = icons[level] || icons.warn;
    const img = $.NSImage.imageWithSystemSymbolNameAccessibilityDescription(pick[0], 'Shuttle Server');
    if (img.isNil()) {
      item.button.image = $();
      item.button.title = pick[1];
    } else {
      img.template = true; // follows light and dark menu bars
      item.button.image = img;
      item.button.title = '';
    }
  }

  function refresh() {
    const s = $.NSString.stringWithContentsOfFileEncodingError(statusPath, $.NSUTF8StringEncoding, $());
    const now = Date.now();
    if (!s.isNil()) {
      // Lines: unix time, level, headline, version, page URL
      const f = s.js.split('\n');
      if (now / 1000 - Number(f[0]) < 15) {
        lastGood = now;
        setIcon(f[1]);
        head.title = 'Shuttle Server ' + f[3] + ' — running';
        line.title = f[2];
        item.button.toolTip = 'Shuttle Server: ' + f[2];
        if (f[4]) pageURL = f[4];
      }
    }
    if (now - lastGood > 15000) app.terminate($()); // the server is gone
  }

  setIcon('ok');
  refresh();
  $.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(2, target, 'tick:', $(), true);
  app.run;
}
