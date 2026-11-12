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
  let lastGood = Date.now(), lastLevel = '', updateState = 'none';
  // What the owner just pressed, shown at once until the server's state moves:
  // { kind: 'check' or 'install', from: state when pressed, at: time }.
  let pending = null;
  // Commands go to Shuttle's own page server, with the header it requires.
  function post(path, body) {
    $.NSTask.launchedTaskWithLaunchPathArguments('/usr/bin/curl', $(['-s', '-m', '10', '-o', '/dev/null', '-X', 'POST',
      '-H', 'X-Shuttle: 1', '-H', 'Content-Type: application/json', '-d', body, pageURL + path]));
  }

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
      'restartServer:': { types: ['void', ['id']], implementation: function () {
        post('/api/restart', '{}');
      } },
      'updateAction:': { types: ['void', ['id']], implementation: function () {
        if (updateState === 'available') {
          pending = { kind: 'install', from: updateState, at: Date.now() };
          showUpdate('Installing update…', false);
          post('/api/update/install', '{}');
        } else if (updateState === 'nokey') {
          menu.cancelTracking; // this one opens the page, so the menu closes
          $.NSWorkspace.sharedWorkspace.openURL($.NSURL.URLWithString(pageURL + '/#updates'));
        }
        else {
          pending = { kind: 'check', from: updateState, at: Date.now() };
          showUpdate('Checking for updates…', false);
          post('/api/update/check', '{}');
        }
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
  add('Open Shuttle', 'openPage:');
  // The update item is a button inside the menu, not a plain menu item: a
  // plain item closes the menu when clicked, and the owner wants to see it
  // change to "Checking for updates…" or "Installing update…" in place.
  const upd = add('Check for updates', null);
  const font = $.NSFont.menuFontOfSize(0);
  const updView = $.NSView.alloc.initWithFrame($.NSMakeRect(0, 0, 260, 22));
  const updButton = $.NSButton.buttonWithTitleTargetAction('Check for updates', target, 'updateAction:');
  updButton.bezelStyle = 13; // recessed: no frame until the pointer is over it
  updButton.showsBorderOnlyWhileMouseInside = true;
  updButton.alignment = 0; // left, like the other items
  updButton.font = font;
  updButton.frame = $.NSMakeRect(8, 0, 244, 22);
  updView.addSubview(updButton);
  upd.view = updView;
  menu.addItem($.NSMenuItem.separatorItem);
  add('Restart Shuttle', 'restartServer:');
  add('Stop Shuttle', 'quitServer:');
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

  function showUpdate(title, enabled) {
    upd.title = title; // read by VoiceOver
    updButton.title = title;
    updButton.enabled = enabled;
    // Grow (never shrink) so a long message is not cut off.
    const w = $(title).sizeWithAttributes($({ NSFont: font })).width + 40;
    if (w > updView.frame.size.width) {
      updView.setFrameSize($.NSMakeSize(w, 22));
      updButton.setFrameSize($.NSMakeSize(w - 16, 22));
    }
  }

  function refresh() {
    const s = $.NSString.stringWithContentsOfFileEncodingError(statusPath, $.NSUTF8StringEncoding, $());
    const now = Date.now();
    if (!s.isNil()) {
      // Lines: unix time, level, headline, version, page URL, update state, update version
      const f = s.js.split('\n');
      if (now / 1000 - Number(f[0]) < 15) {
        lastGood = now;
        setIcon(f[1]);
        head.title = 'Shuttle Server ' + f[3] + ' — running';
        line.title = f[2];
        item.button.toolTip = 'Shuttle Server: ' + f[2];
        if (f[4]) pageURL = f[4];
        updateState = f[5] || 'none';
        const titles = {
          none: ['Check for updates', true],
          nokey: ['Set up updates…', true],
          checking: ['Checking for updates…', false],
          downloading: ['Downloading update…', false],
          installing: ['Installing update…', false],
          failed: ['Update did not work — open Shuttle for why; try again', true],
          waiting: ['Update installs when the copy finishes', false],
          available: ['Install update ' + (f[6] || ''), true],
        };
        const t = titles[updateState] || titles.none;
        const age = pending ? now - pending.at : 0;
        const working = updateState === 'checking' || updateState === 'downloading' || updateState === 'installing';
        if (pending && !working && pending.kind === 'check' && updateState === pending.from && age < 4000) {
          showUpdate('Checking for updates…', false); // pressed; the server has not picked it up yet
        } else if (pending && !working && pending.kind === 'check' && updateState === 'none' && age < 20000) {
          showUpdate('Shuttle is up to date', false); // the check found nothing newer
        } else if (pending && !working && pending.kind === 'install' && updateState === 'available' && age < 60000) {
          showUpdate('Installing update…', false);
        } else {
          if (!working) pending = null;
          showUpdate(t[0], t[1]);
        }
      }
    }
    if (now - lastGood > 15000) app.terminate($()); // the server is gone
  }

  setIcon('ok');
  refresh();
  const timer = $.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(2, target, 'tick:', $(), true);
  // Keep refreshing while the menu is open, so the update item moves on by itself.
  $.NSRunLoop.currentRunLoop.addTimerForMode(timer, $.NSRunLoopCommonModes);
  app.run;
}
