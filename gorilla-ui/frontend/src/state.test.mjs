import assert from "node:assert/strict";
import test from "node:test";

import {
  ALL_CATEGORIES,
  bannerMessage,
  categories,
  categoryGlyph,
  compareItems,
  deriveAction,
  fromCache,
  monogram,
  restartBadge,
  showRetry,
  statusLabel,
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
