import assert from "node:assert/strict";
import test from "node:test";

import {
  ALL_CATEGORIES,
  ERROR_STATE,
  STREAM_ENDED_STATE,
  activityLine,
  bannerMessage,
  cardProgress,
  categories,
  categoryGlyph,
  compareItems,
  deriveAction,
  fromCache,
  isItemActive,
  isItemPhase,
  isTerminalState,
  localErrorState,
  localRecord,
  monogram,
  progressLabel,
  releaseOperation,
  restartBadge,
  shouldAcceptRecord,
  showRetry,
  stateLabel,
  statusLabel,
  statusRecord,
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
  assert.equal(statusLabel(item({ status: "WillBeInstalled" })), "Will Be Installed");
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

test("a record arriving after the terminal one is dropped, not shown as current", () => {
  // Mirrors the onOperationStatus handler: Wails can deliver an ItemCompleted
  // after the terminal record, and appending it would leave a finished
  // operation reading "Installing 55%" as its newest line.
  const operation = { records: [], outcome: "active" };
  const activity = [];
  const apply = (status) => {
    if (!shouldAcceptRecord(operation.outcome)) {
      return;
    }
    const record = statusRecord(status);
    operation.records.push(record);
    activity.unshift(record);
    if (isTerminalState(record.state)) {
      operation.outcome = "terminal";
    }
  };

  const event = (state, progressPercent) => ({
    operationId: "op-1",
    timestampUtc: "2026-07-21T12:00:00Z",
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state,
    progressPercent,
    message: state,
  });

  apply(event("Installing", 55));
  apply(event("Succeeded", 100));
  apply(event("ItemCompleted", 55));

  assert.deepEqual(operation.records.map((r) => r.state), ["Installing", "Succeeded"]);
  assert.equal(operation.records[operation.records.length - 1].state, "Succeeded");
  assert.deepEqual(activity.map((r) => r.state), ["Succeeded", "Installing"]);
  assert.equal(shouldAcceptRecord("active"), true);
  assert.equal(shouldAcceptRecord("error"), false, "a failed operation stops accepting late records too");
});

test("status records keep the event's own item identity and percentage", () => {
  const dependency = statusRecord({
    operationId: "op-1",
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
  assert.equal(statusLabel(item({ status: "WillBeRemoved" })), "Will Be Removed");
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
  // Queued/Requested: a line, no percentage (indeterminate bar).
  assert.deepEqual(cardProgress(chrome, [record({ state: "Requested" })], "active"), {
    label: "Requested…",
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
    { label: "Deferred — blocking application(s) running: chrome", outcome: "terminal" },
  );
  assert.deepEqual(cardProgress(chrome, [record({ state: ERROR_STATE, message: "pipe closed" })], "error"), {
    label: "Request failed — pipe closed",
    outcome: "error",
  });
});
