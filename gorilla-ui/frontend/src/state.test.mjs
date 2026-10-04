import assert from "node:assert/strict";
import test from "node:test";

import {
  ALL_CATEGORIES,
  REQUIRED_LABEL,
  REQUESTED_STATE,
  ERROR_STATE,
  STREAM_ENDED_STATE,
  activityLine,
  addActivity,
  bannerMessage,
  connectionLabel,
  brandingView,
  canCancel,
  cancelTitle,
  cardProgress,
  categories,
  categoryGlyph,
  compareItems,
  deriveAction,
  fromCache,
  glyphTone,
  httpUrl,
  insertRecord,
  isHexColor,
  isItemActive,
  isItemPhase,
  isTerminalState,
  localErrorState,
  localRecord,
  monogram,
  myItems,
  onAccent,
  progressLabel,
  progressText,
  refusalText,
  releaseOperation,
  restartBadge,
  shouldAcceptRecord,
  showRetry,
  stateLabel,
  statusLabel,
  statusRecord,
  stripLine,
  stripView,
  trackOperation,
  visibleItems,
  withFailure,
  withLive,
} from "./state.ts";

function item(overrides) {
  return {
    itemName: "Item",
    displayName: "Item",
    version: "1.0.0",
    catalog: "catalog",
    isManaged: false,
    isInstalled: false,
    status: "NotInstalled",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
    ...overrides,
  };
}

const catalogItems = [
  item({ itemName: "zed", displayName: "Zed Editor", category: "Development", developer: "Zed" }),
  item({ itemName: "acme", displayName: "acme reader", category: "Productivity" }),
  item({ itemName: "brave", displayName: "Brave", category: "browsers", developer: "Acme Corp" }),
];

test("search is case-insensitive across name, item, developer and category", () => {
  assert.deepEqual(
    visibleItems(catalogItems, "ACME", ALL_CATEGORIES).map((i) => i.itemName),
    ["acme", "brave"],
  );
  assert.deepEqual(
    visibleItems(catalogItems, "development", ALL_CATEGORIES).map((i) => i.itemName),
    ["zed"],
  );
  assert.equal(visibleItems(catalogItems, "   ", ALL_CATEGORIES).length, 3);
  assert.equal(visibleItems(catalogItems, "nothing-here", ALL_CATEGORIES).length, 0);
});

test("category filter is case-insensitive and 'all' keeps everything", () => {
  assert.deepEqual(
    visibleItems(catalogItems, "", "BROWSERS").map((i) => i.itemName),
    ["brave"],
  );
  assert.equal(visibleItems(catalogItems, "", ALL_CATEGORIES).length, 3);
});

test("card order is stable, case-insensitive by display name then item name", () => {
  assert.deepEqual(
    visibleItems(catalogItems, "", ALL_CATEGORIES).map((i) => i.displayName),
    ["acme reader", "Brave", "Zed Editor"],
  );
  const tie = [item({ itemName: "b", displayName: "Same" }), item({ itemName: "a", displayName: "same" })];
  assert.deepEqual(tie.slice().sort(compareItems).map((i) => i.itemName), ["a", "b"]);
});

test("filtering does not mutate the source list", () => {
  const source = catalogItems.slice();
  visibleItems(source, "", ALL_CATEGORIES);
  assert.deepEqual(source.map((i) => i.itemName), ["zed", "acme", "brave"]);
});

test("categories are unique, non-empty and sorted", () => {
  const withDuplicates = [...catalogItems, item({ itemName: "x", category: "development" }), item({ itemName: "y" })];
  assert.deepEqual(categories(withDuplicates), ["browsers", "Development", "Productivity"]);
});

test("action derivation covers every status and managed combination", () => {
  const cases = [
    ["WillBeInstalled", false, "Cancel", "RemoveItem"],
    ["WillBeInstalled", true, "Cancel", "RemoveItem"],
    ["WillBeRemoved", false, "Cancel", "InstallItem"],
    ["WillBeRemoved", true, "Cancel", "InstallItem"],
    ["Installed", true, "Remove", "RemoveItem"],
    ["Installed", false, "Install", "InstallItem"],
    ["NotInstalled", true, "Remove", "RemoveItem"],
    ["NotInstalled", false, "Install", "InstallItem"],
    ["Unknown", true, "Remove", "RemoveItem"],
    ["Unknown", false, "Install", "InstallItem"],
    ["", true, "Remove", "RemoveItem"],
    ["", false, "Install", "InstallItem"],
  ];
  for (const [status, isManaged, label, method] of cases) {
    assert.deepEqual(
      deriveAction(item({ status, isManaged })),
      { label, method },
      `${status || "(blank)"} isManaged=${isManaged}`,
    );
  }
});

// The service refuses to remove an item an admin manifest requires, so the UI
// must not offer a button that can only fail; it says who manages it instead.
test("a required item has no action and says the organisation manages it", () => {
  for (const status of ["Installed", "NotInstalled", "WillBeInstalled", "WillBeRemoved", "Unknown"]) {
    for (const isManaged of [false, true]) {
      const required = item({ status, isManaged, isRequired: true });
      assert.equal(deriveAction(required), null, `${status} isManaged=${isManaged}`);
      assert.equal(statusLabel(required), "Managed by your organisation");
    }
  }
  assert.equal(REQUIRED_LABEL, "Managed by your organisation");
  assert.deepEqual(deriveAction(item({ status: "Installed", isManaged: true, isRequired: false })), {
    label: "Remove",
    method: "RemoveItem",
  });
});

test("monogram uses initials and falls back to a category glyph", () => {
  assert.equal(monogram(item({ displayName: "Demo Optional Package" })), "DO");
  assert.equal(monogram(item({ displayName: "Brave" })), "B");
  assert.equal(monogram(item({ displayName: "7-Zip" })), "7Z");
  assert.equal(monogram(item({ displayName: "", itemName: "fallback" })), "F");
  assert.equal(monogram(item({ displayName: "***", itemName: "***", category: "Security" })), "🛡");
  assert.equal(monogram(item({ displayName: "***", itemName: "***" })), "▪");
  assert.equal(categoryGlyph(undefined), "▪");
});

test("restart badge is only shown when restartAction is meaningful", () => {
  // "" means the badge element stays hidden; a stray empty pill is a rendering bug.
  assert.equal(restartBadge(item({})), "");
  assert.equal(restartBadge(item({ restartAction: undefined })), "");
  assert.equal(restartBadge(item({ restartAction: "" })), "");
  assert.equal(restartBadge(item({ restartAction: "  " })), "");
  assert.equal(restartBadge(item({ restartAction: "none" })), "");
  assert.equal(restartBadge(item({ restartAction: "None" })), "");
  assert.equal(restartBadge(item({ restartAction: "RequireRestart" })), "Restart required");
  assert.equal(restartBadge(item({ restartAction: "RecommendRestart" })), "Restart recommended");
  assert.equal(restartBadge(item({ restartAction: "SomethingElse" })), "SomethingElse");
});

test("status label stays readable for blank and camel-case values", () => {
  assert.equal(statusLabel(item({ status: "WillBeInstalled" })), "Will be installed");
  assert.equal(statusLabel(item({ status: "NotInstalled" })), "Not installed");
  assert.equal(statusLabel(item({ status: "Installed" })), "Installed");
  assert.equal(statusLabel(item({ status: "" })), "Unknown");
});

test("list view transitions from cache to live and back to stale", () => {
  const empty = fromCache(null);
  assert.equal(empty.source, "loading");
  assert.equal(showRetry(empty), false);

  const cached = fromCache({ savedAtUtc: "2026-07-21T12:00:00Z", items: catalogItems });
  assert.equal(cached.source, "cache");
  assert.equal(cached.items.length, 3);
  assert.match(bannerMessage(cached, ""), /cached/i);
  assert.equal(showRetry(cached), false);

  const live = withLive([catalogItems[0]], "2026-07-22T00:00:00Z");
  assert.deepEqual(live, {
    items: [catalogItems[0]],
    source: "live",
    savedAtUtc: "2026-07-22T00:00:00Z",
  });
  assert.equal(showRetry(live), false);

  const stale = withFailure(live);
  assert.equal(stale.source, "stale");
  assert.deepEqual(stale.items, live.items, "a failed refresh keeps the previous items");
  assert.equal(stale.savedAtUtc, live.savedAtUtc);
  assert.equal(showRetry(stale), true);
  assert.match(
    bannerMessage(stale, "pipe unavailable"),
    /^Service unavailable — showing cached software from .*\. Reason: pipe unavailable$/,
  );
  assert.equal(
    bannerMessage(stale, ""),
    bannerMessage(stale, "pipe unavailable").replace(" Reason: pipe unavailable", ""),
    "a blank error leaves no dangling label",
  );

  const staleEmpty = withFailure(empty);
  assert.equal(
    bannerMessage(staleEmpty, "pipe unavailable"),
    "Service unavailable and no cached software is stored. Reason: pipe unavailable",
  );
  assert.equal(showRetry(staleEmpty), true);
});

test("only Succeeded, Failed, Deferred and Canceled end an operation", () => {
  for (const state of ["Succeeded", "Failed", "Deferred", "Canceled"]) {
    assert.equal(isTerminalState(state), true, state);
  }
  // A dependency failing or finishing must never be read as the operation result.
  for (const state of ["Queued", "Downloading", "Installing", "Removing", "ItemCompleted", "ItemFailed", "Requested", "", "succeeded"]) {
    assert.equal(isTerminalState(state), false, state || "(blank)");
  }
});

test("a determinate bar is only offered for item-phase records", () => {
  for (const state of ["Downloading", "Installing", "Removing"]) {
    assert.equal(isItemPhase(state), true, state);
  }
  for (const state of ["Queued", "ItemCompleted", "ItemFailed", "Succeeded", "Failed", "Deferred", "Canceled", ""]) {
    assert.equal(isItemPhase(state), false, state || "(blank)");
  }
});

test("active operations disable only their own item and route independently", () => {
  let active = new Map();
  active = trackOperation(active, "op-1", "DemoOptional");
  active = trackOperation(active, "op-2", "DemoFailing");

  assert.equal(isItemActive(active, "DemoOptional"), true);
  assert.equal(isItemActive(active, "DemoFailing"), true);
  assert.equal(isItemActive(active, "DemoBlocked"), false);

  const afterFirst = releaseOperation(active, "op-1");
  assert.equal(isItemActive(afterFirst, "DemoOptional"), false);
  assert.equal(isItemActive(afterFirst, "DemoFailing"), true, "a concurrent operation keeps its own item busy");
  assert.equal(isItemActive(active, "DemoOptional"), true, "release does not mutate the previous map");
  assert.equal(afterFirst.get("op-2"), "DemoFailing");
  assert.equal(releaseOperation(afterFirst, "op-unknown").size, 1);
});

test("records are filed by seq whatever order they arrive in", () => {
  // Mirrors the onOperationStatus handler: Wails can deliver records out of
  // order, even after the terminal one, and a finished operation must not
  // read "Installing 55%" as its newest line.
  const operation = { records: [], outcome: "active" };
  const apply = (status) => {
    if (!shouldAcceptRecord(operation.outcome)) {
      return false;
    }
    const record = statusRecord(status);
    if (!insertRecord(operation.records, record)) {
      return false;
    }
    if (isTerminalState(record.state)) {
      operation.outcome = "terminal";
    }
    return true;
  };

  const event = (seq, state, progressPercent) => ({
    operationId: "op-1",
    seq,
    timestampUtc: "2026-07-21T12:00:00.020Z",
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state,
    progressPercent,
    message: state,
  });

  insertRecord(operation.records, localRecord("op-1", event(0, "", 0), REQUESTED_STATE, "accepted", "2026-07-21T12:00:00.000Z"));
  apply(event(1, "Queued", 0));
  apply(event(3, "Installing", 55));
  apply(event(4, "Succeeded", 100));
  apply(event(2, "Downloading", 0));
  assert.equal(apply(event(3, "Installing", 55)), false, "a repeated seq is dropped");

  assert.deepEqual(
    operation.records.map((r) => r.state),
    [REQUESTED_STATE, "Queued", "Downloading", "Installing", "Succeeded"],
  );
  assert.equal(operation.outcome, "terminal");
  assert.equal(shouldAcceptRecord("active"), true);
  assert.equal(shouldAcceptRecord("terminal"), true, "a late record still belongs in the finished timeline");
  assert.equal(shouldAcceptRecord("error"), false, "after a local error its own record stays the latest");
});

test("Activity is newest first by time, then by seq within an operation", () => {
  const record = (operationId, seq, timestampUtc, state) => ({
    operationId,
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state,
    message: "",
    timestampUtc,
    ...(seq === undefined ? {} : { seq }),
  });
  let activity = [];
  // Arrival order, as Wails might deliver it: a batch inverted, then a
  // record of another operation, then a late one.
  activity = addActivity(activity, record("op-1", undefined, "2026-07-21T12:00:00.000Z", REQUESTED_STATE), 100);
  activity = addActivity(activity, record("op-1", 2, "2026-07-21T12:00:01.500Z", "Downloading"), 100);
  activity = addActivity(activity, record("op-1", 1, "2026-07-21T12:00:01.500Z", "Queued"), 100);
  activity = addActivity(activity, record("op-2", 1, "2026-07-21T12:00:02.000Z", "Queued"), 100);
  activity = addActivity(activity, record("op-1", 3, "2026-07-21T12:00:03.000Z", "Succeeded"), 100);

  assert.deepEqual(
    activity.map((r) => `${r.operationId}:${r.state}`),
    ["op-1:Succeeded", "op-2:Queued", "op-1:Downloading", "op-1:Queued", `op-1:${REQUESTED_STATE}`],
  );
  assert.equal(addActivity(activity, record("op-3", 1, "2026-07-21T12:00:04.000Z", "Queued"), 2).length, 2);
});

test("status records keep the event's own item identity and percentage", () => {
  const dependency = statusRecord({
    operationId: "op-1",
    seq: 5,
    timestampUtc: "2026-07-21T12:00:05Z",
    itemName: "DemoUpdater",
    displayName: "Demo Updater",
    state: "Installing",
    progressPercent: 10,
    message: "Installing DemoUpdater",
  });
  assert.deepEqual(dependency, {
    operationId: "op-1",
    itemName: "DemoUpdater",
    displayName: "Demo Updater",
    state: "Installing",
    message: "Installing DemoUpdater",
    timestampUtc: "2026-07-21T12:00:05Z",
    seq: 5,
    progressPercent: 10,
  });
  assert.equal(progressLabel(dependency), "Demo Updater — Installing");

  const blankName = statusRecord({
    operationId: "op-1",
    timestampUtc: "2026-07-21T12:00:06Z",
    itemName: "DemoOptional",
    displayName: "",
    state: "ItemFailed",
    progressPercent: 0,
    message: "install failed",
    errorMessage: "installer exited with code 1",
  });
  assert.equal(blankName.displayName, "DemoOptional");
  assert.equal(blankName.message, "install failed (installer exited with code 1)");
  assert.equal(blankName.detail, "installer exited with code 1", "the bare error is kept for outcome lines");
  assert.equal(isTerminalState(blankName.state), false, "ItemFailed stays non-terminal");

  const duplicated = statusRecord({
    operationId: "op-1",
    timestampUtc: "2026-07-21T12:00:07Z",
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state: "Failed",
    progressPercent: 100,
    message: "boom",
    errorMessage: "boom",
  });
  assert.equal(duplicated.message, "boom", "an identical errorMessage is not repeated");
});

test("local records label request failures and premature stream ends", () => {
  assert.equal(localErrorState("operation stream ended before a terminal event"), STREAM_ENDED_STATE);
  assert.equal(localErrorState("pipe unavailable"), ERROR_STATE);

  const record = localRecord(
    "local-1",
    { itemName: "DemoOptional", displayName: "" },
    ERROR_STATE,
    "pipe unavailable",
    "2026-07-21T12:00:00Z",
  );
  assert.deepEqual(record, {
    operationId: "local-1",
    itemName: "DemoOptional",
    displayName: "DemoOptional",
    state: ERROR_STATE,
    message: "pipe unavailable",
    timestampUtc: "2026-07-21T12:00:00Z",
  });
  assert.equal(record.progressPercent, undefined, "a local record carries no service percentage");
});

test("state labels stay readable for wire and local states", () => {
  assert.equal(stateLabel("ItemCompleted"), "Item Completed");
  assert.equal(stateLabel(STREAM_ENDED_STATE), "Stream ended before a result");
  assert.equal(stateLabel(ERROR_STATE), "Request failed");
  assert.equal(stateLabel(""), "Unknown");
  assert.equal(statusLabel(item({ status: "WillBeRemoved" })), "Will be removed");
});

test("activity lines name the item, state and message", () => {
  const line = activityLine({
    operationId: "op-1",
    itemName: "DemoUpdater",
    displayName: "Demo Updater",
    state: "ItemCompleted",
    message: "done",
    timestampUtc: "not-a-date",
  });
  assert.equal(line, "not-a-date — Demo Updater: Item Completed — done");
  assert.equal(
    activityLine({
      operationId: "op-1",
      itemName: "DemoUpdater",
      displayName: "",
      state: "Queued",
      message: "  ",
      timestampUtc: "not-a-date",
    }),
    "not-a-date — DemoUpdater: Queued",
  );
});

test("card progress shows one line and a bar while active, then only outcomes worth acting on", () => {
  const chrome = { itemName: "GoogleChrome" };
  const record = (overrides) => ({
    operationId: "op-1",
    itemName: "GoogleChrome",
    displayName: "Google Chrome",
    state: "Installing",
    message: "",
    timestampUtc: "2026-10-03T22:22:44Z",
    ...overrides,
  });

  assert.equal(cardProgress(chrome, [], "active"), null);
  // Requested/Queued: the service has not started on it yet.
  assert.deepEqual(cardProgress(chrome, [record({ state: "Requested" })], "active"), {
    label: "Waiting…",
    outcome: "active",
  });
  // Own item phase carries its percentage.
  assert.deepEqual(cardProgress(chrome, [record({ progressPercent: 50 })], "active"), {
    label: "Installing…",
    percent: 50,
    outcome: "active",
  });
  // Another item's phase (updater, dependency, or an unrelated item in the
  // same run) is named so its percentage is not read as this item's.
  assert.deepEqual(
    cardProgress(
      chrome,
      [record({ itemName: "DemoFailing", displayName: "Demo Failing", progressPercent: 50 })],
      "active",
    ),
    { label: "Installing Demo Failing…", percent: 50, outcome: "active" },
  );
  assert.deepEqual(
    cardProgress(chrome, [record({ itemName: "DemoFailing", displayName: "Demo Failing", state: "ItemFailed" })], "active"),
    { label: "In progress…", outcome: "active" },
  );
  // Success leaves the card to the refreshed list status.
  assert.equal(
    cardProgress(chrome, [record({ state: "Succeeded", message: "Operation completed" })], "terminal"),
    null,
  );
  // Deferred/Failed keep their reason on the card until the next action.
  assert.deepEqual(
    cardProgress(chrome, [record({ state: "Deferred", message: "blocking application(s) running: chrome" })], "terminal"),
    { label: "Waiting: close chrome to continue", outcome: "terminal" },
  );
  assert.deepEqual(cardProgress(chrome, [record({ state: ERROR_STATE, message: "pipe closed" })], "error"), {
    label: "Request failed · pipe closed",
    outcome: "error",
  });
});

test("outcome lines say what failed and quote the service's reason", () => {
  const failed = {
    operationId: "op-1",
    itemName: "DemoFailing",
    displayName: "Demo Failing",
    state: "Failed",
    message: "Operation failed (installer exited with code 1)",
    detail: "installer exited with code 1",
    timestampUtc: "2026-10-03T22:22:44Z",
  };
  const item = { itemName: "DemoFailing" };
  assert.equal(
    cardProgress(item, [failed], "terminal", "InstallItem").label,
    "Install failed · installer exited with code 1",
  );
  assert.equal(
    cardProgress(item, [failed], "terminal", "RemoveItem").label,
    "Removal failed · installer exited with code 1",
  );
  // Without a wire errorMessage the message itself is the reason.
  assert.equal(
    cardProgress(item, [{ ...failed, detail: undefined, message: "boom" }], "terminal").label,
    "Install failed · boom",
  );
  // A deferral that is not about a running app reads as the service's reason.
  assert.equal(
    cardProgress(item, [{ ...failed, state: "Deferred", detail: undefined, message: "dependency DemoUpdater deferred" }], "terminal").label,
    "dependency DemoUpdater deferred",
  );
  assert.equal(
    cardProgress(item, [{ ...failed, state: "Canceled", detail: undefined, message: "Operation canceled" }], "terminal").label,
    "Canceled",
  );
});

test("My items keeps only installed or managed items", () => {
  const items = [
    item({ itemName: "a", isInstalled: true }),
    item({ itemName: "b", isManaged: true, status: "WillBeInstalled" }),
    item({ itemName: "c" }),
  ];
  assert.deepEqual(myItems(items).map((i) => i.itemName), ["a", "b"]);
});

test("glyph tone is stable per item and inside the palette", () => {
  assert.equal(glyphTone({ itemName: "GoogleChrome" }), glyphTone({ itemName: "GoogleChrome" }));
  for (const name of ["", "a", "GoogleChrome", "DemoOptional", "7-Zip"]) {
    const tone = glyphTone({ itemName: name });
    assert.ok(Number.isInteger(tone) && tone >= 0 && tone < 5, `${name} -> ${tone}`);
  }
});

test("the strip shows the running operation the service is working on, as n of N", () => {
  const record = (operationId, itemName, state, progressPercent) => ({
    operationId,
    itemName,
    displayName: itemName,
    state,
    message: "",
    timestampUtc: "2026-10-03T22:22:44Z",
    ...(progressPercent === undefined ? {} : { progressPercent }),
  });
  const operation = (operationId, itemName, outcome, records) => ({
    operationId,
    item: item({ itemName, displayName: itemName }),
    action: { label: "Install", method: "InstallItem" },
    records,
    outcome,
  });

  assert.equal(stripView([]), null, "nothing running hides the strip");
  assert.equal(
    stripView([operation("op-0", "Done", "terminal", [record("op-0", "Done", "Succeeded")])]),
    null,
    "finished operations never hold the strip open",
  );

  // Both just accepted: the earliest is shown, with no percentage (indeterminate).
  const waiting = stripView([
    operation("op-1", "Chrome", "active", [record("op-1", "Chrome", REQUESTED_STATE)]),
    operation("op-2", "Zed", "active", [record("op-2", "Zed", "Queued")]),
  ]);
  assert.equal(waiting.operation.operationId, "op-1");
  assert.deepEqual([waiting.position, waiting.total], [1, 2]);
  assert.equal(waiting.progress.percent, undefined);
  assert.equal(stripLine(waiting), "Waiting… · 1 of 2");

  // The second one is the one actually reporting: it is shown as 2 of 2 with
  // its percentage; the first stays "Waiting…" on its own card.
  const working = stripView([
    operation("op-0", "Done", "terminal", [record("op-0", "Done", "Failed")]),
    operation("op-1", "Chrome", "active", [record("op-1", "Chrome", REQUESTED_STATE), record("op-1", "Chrome", "Queued")]),
    operation("op-2", "Zed", "active", [record("op-2", "Zed", REQUESTED_STATE), record("op-2", "Zed", "Downloading", 42)]),
  ]);
  assert.equal(working.operation.operationId, "op-2");
  assert.deepEqual([working.position, working.total], [2, 2]);
  assert.equal(working.progress.percent, 42);
  assert.equal(stripLine(working), "Downloading… 42% · 2 of 2");

  // A phase without a percentage on the wire stays indeterminate.
  const unmeasured = stripView([
    operation("op-3", "Chrome", "active", [record("op-3", "Chrome", "ItemCompleted")]),
  ]);
  assert.equal(unmeasured.progress.percent, undefined);
  assert.equal(progressText(unmeasured.progress), "Item Completed…");
});

// With nothing configured the shell must look exactly as it does unbranded.
test("brandingView of an empty or missing payload is the default shell", () => {
  for (const payload of [null, undefined, "junk", {}, { title: "  ", accent: "", helpUrl: "" }]) {
    const view = brandingView(payload);
    assert.equal(view.showBanner, false);
    assert.equal(view.productName, "Gorilla");
    assert.equal(view.productMark, "G");
    assert.equal(view.accent, "");
    assert.equal(view.logoSrc, "");
    assert.equal(view.helpUrl, "");
  }
});

test("brandingView shows the banner for any of title, tagline, logo or help", () => {
  const png = { logoMime: "image/png", logoBase64: "iVBORw0KGgo=" };
  for (const payload of [{ title: "Acme" }, { tagline: "Call ext. 1234" }, png, { helpUrl: "https://example.invalid/help" }]) {
    assert.equal(brandingView(payload).showBanner, true, JSON.stringify(payload));
  }
  // An accent alone recolours the shell but is not banner content.
  assert.equal(brandingView({ accent: "#0b6e4f" }).showBanner, false);
  // A help label without a URL has nothing to open.
  assert.equal(brandingView({ helpLabel: "Get help" }).showBanner, false);
});

test("brandingView maps the branded board", () => {
  const view = brandingView({
    title: "Acme Software Center",
    tagline: "Need help? Call the service desk at ext. 1234.",
    helpUrl: "https://example.invalid/help",
    helpLabel: "",
    accent: "#0B6E4F",
    logoMime: "image/png",
    logoBase64: "iVBORw0KGgo=",
  });
  assert.equal(view.productName, "Acme Software Center");
  assert.equal(view.productMark, "A");
  assert.equal(view.logoSrc, "data:image/png;base64,iVBORw0KGgo=");
  assert.equal(view.helpLabel, "Get help");
  assert.equal(view.accent, "#0b6e4f");
  assert.equal(view.onAccent, "#ffffff");
});

// The WebView only ever opens http(s), builds an image data: URL, or sets a
// #rrggbb colour, even if a cached or tampered payload says otherwise.
test("brandingView drops values the WebView must not use", () => {
  const view = brandingView({
    title: "Acme",
    helpUrl: "javascript:alert(1)",
    helpLabel: "Click",
    accent: "red; --x: url(evil)",
    logoMime: "text/html",
    logoBase64: "PHNjcmlwdD4=",
  });
  assert.equal(view.helpUrl, "");
  assert.equal(view.helpLabel, "");
  assert.equal(view.accent, "");
  assert.equal(view.logoSrc, "");
  assert.equal(brandingView({ logoMime: "image/svg+xml", logoBase64: '"><script>' }).logoSrc, "");
});

test("isHexColor and httpUrl", () => {
  assert.equal(isHexColor("#0b6e4f"), true);
  assert.equal(isHexColor("#0b6e4"), false);
  assert.equal(isHexColor("0b6e4f"), false);
  assert.equal(httpUrl("https://example.invalid/help"), "https://example.invalid/help");
  assert.equal(httpUrl("HTTP://intranet/help"), "http://intranet/help");
  assert.equal(httpUrl("file:///C:/Windows"), "");
  assert.equal(httpUrl("/help"), "");
});

test("onAccent picks readable text", () => {
  assert.equal(onAccent("#0b6e4f"), "#ffffff");
  assert.equal(onAccent("#1a5fb4"), "#ffffff");
  assert.equal(onAccent("#ffd400"), "#1b1b1f");
  assert.equal(onAccent("#ffffff"), "#1b1b1f");
});

test("Cancel is offered only before the installer starts", () => {
  for (const state of [REQUESTED_STATE, "Queued", "Downloading"]) {
    assert.equal(canCancel(state), true, state);
    assert.equal(cancelTitle(state), "Cancel before the installer starts");
  }
  // A running installer is never interrupted, and finished work has nothing to cancel.
  for (const state of ["Installing", "Removing", "ItemCompleted", "ItemFailed", "Succeeded", "Canceled", ""]) {
    assert.equal(canCancel(state), false, state);
    assert.match(cancelTitle(state), /^Can't cancel now/);
  }
  assert.equal(cancelTitle("Queued", true), "Canceling…");
});

test("a refused cancel shows the service's reason without its error code", () => {
  assert.equal(
    refusalText("operation_not_cancelable: operation can no longer be canceled: work on Google Chrome has already started"),
    "Operation can no longer be canceled: work on Google Chrome has already started",
  );
  assert.equal(refusalText("pipe unavailable"), "Pipe unavailable");
  assert.equal(refusalText(""), "The operation can no longer be canceled");
});

test("a user cancel reads plainly on the card and says who in Activity", () => {
  const chrome = { itemName: "GoogleChrome" };
  const canceled = statusRecord({
    operationId: "op-1",
    timestampUtc: "not-a-date",
    itemName: "GoogleChrome",
    displayName: "Google Chrome",
    state: "Canceled",
    progressPercent: 0,
    message: "Canceled by user",
    canceledBy: "user",
  });
  assert.equal(canceled.canceledBy, "user");
  assert.deepEqual(cardProgress(chrome, [canceled], "terminal"), {
    label: "Canceled",
    outcome: "terminal",
    plain: true,
  });
  assert.equal(activityLine(canceled), "not-a-date — Google Chrome: Canceled by you");
  assert.equal(
    activityLine({ ...canceled, canceledBy: "service", message: "Operation canceled" }),
    "not-a-date — Google Chrome: Canceled by the service",
  );

  // A refused cancel replaces the running line, not the item's state.
  const installing = { ...canceled, state: "Installing", canceledBy: undefined };
  assert.deepEqual(cardProgress(chrome, [installing], "active", "InstallItem", "Work has already started"), {
    label: "Work has already started",
    outcome: "active",
  });
  // Other outcomes keep the warning treatment.
  assert.equal(cardProgress(chrome, [{ ...installing, state: "Failed" }], "terminal").plain, undefined);
});

test("the connection label is short in every state while the message keeps the reason", () => {
  const stale = { items: [item({})], source: "stale", savedAtUtc: "2026-10-03T12:00:00Z" };
  assert.equal(connectionLabel(stale), "Service unavailable");
  assert.match(bannerMessage(stale, "pipe unavailable"), /pipe unavailable/);
  assert.equal(connectionLabel({ items: [], source: "live", savedAtUtc: "" }), "Service connected");
  assert.equal(connectionLabel({ items: [], source: "loading", savedAtUtc: "" }), "Connecting…");
  assert.equal(connectionLabel({ items: [], source: "cache", savedAtUtc: "" }), "Showing cached software");
});
