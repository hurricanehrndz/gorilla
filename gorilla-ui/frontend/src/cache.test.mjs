import assert from "node:assert/strict";
import test from "node:test";

import {
  ACTIVITY_KEY,
  ACTIVITY_LIMIT,
  BRANDING_KEY,
  LIST_KEY,
  loadActivity,
  loadBranding,
  loadList,
  saveActivity,
  saveBranding,
  saveList,
} from "./cache.ts";

function memoryStorage(initial = {}) {
  const data = { ...initial };
  return {
    data,
    getItem: (key) => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = value;
    },
  };
}

const brokenStorage = {
  getItem() {
    throw new DOMException("storage disabled");
  },
  setItem() {
    throw new DOMException("quota exceeded");
  },
};

const sampleItem = {
  itemName: "DemoOptional",
  displayName: "Demo Optional",
  version: "1.0.0",
  catalog: "selfserve_catalog",
  isManaged: false,
  isInstalled: false,
  status: "NotInstalled",
  statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
};

test("a saved list round-trips under the versioned key with its timestamp", () => {
  const storage = memoryStorage();
  saveList(storage, [sampleItem], "2026-07-21T12:00:00Z");
  assert.ok(storage.data[LIST_KEY], "uses gorilla.optional-items.v1");
  assert.deepEqual(loadList(storage), { savedAtUtc: "2026-07-21T12:00:00Z", items: [sampleItem] });
});

test("corrupt or wrongly shaped list caches are ignored", () => {
  for (const raw of [
    "not json at all",
    "[]",
    '"a string"',
    "null",
    JSON.stringify({ items: [sampleItem] }),
    JSON.stringify({ savedAtUtc: 12345, items: [sampleItem] }),
    JSON.stringify({ savedAtUtc: "2026-07-21T12:00:00Z", items: "nope" }),
    JSON.stringify({ savedAtUtc: "2026-07-21T12:00:00Z", items: [{ itemName: "only" }] }),
    JSON.stringify({ savedAtUtc: "2026-07-21T12:00:00Z", items: [null] }),
  ]) {
    assert.equal(loadList(memoryStorage({ [LIST_KEY]: raw })), null, raw);
  }
});

test("a missing list cache reads as null, not an error", () => {
  assert.equal(loadList(memoryStorage()), null);
});

test("unavailable storage never throws into the caller", () => {
  assert.equal(loadList(brokenStorage), null);
  assert.deepEqual(loadActivity(brokenStorage), []);
  assert.doesNotThrow(() => saveList(brokenStorage, [sampleItem], "2026-07-21T12:00:00Z"));
  assert.doesNotThrow(() => saveActivity(brokenStorage, []));
});

function record(index) {
  return {
    operationId: `op-${index}`,
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state: "Succeeded",
    message: "",
    timestampUtc: `2026-07-21T12:00:${String(index).padStart(2, "0")}Z`,
  };
}

test("activity keeps the newest records first and caps at 100", () => {
  const storage = memoryStorage();
  const newestFirst = Array.from({ length: 150 }, (_, i) => record(149 - i));
  saveActivity(storage, newestFirst);
  assert.ok(storage.data[ACTIVITY_KEY], "uses gorilla.activity.v1");

  const loaded = loadActivity(storage);
  assert.equal(loaded.length, ACTIVITY_LIMIT);
  assert.equal(loaded[0].operationId, "op-149", "newest record is retained first");
  assert.equal(loaded.at(-1).operationId, "op-50", "oldest records past the cap are dropped");
});

test("corrupt activity caches degrade to the valid records only", () => {
  assert.deepEqual(loadActivity(memoryStorage({ [ACTIVITY_KEY]: "{" })), []);
  assert.deepEqual(loadActivity(memoryStorage({ [ACTIVITY_KEY]: JSON.stringify({}) })), []);
  assert.deepEqual(
    loadActivity(memoryStorage({ [ACTIVITY_KEY]: JSON.stringify([record(1), null, { state: "Failed" }]) })),
    [record(1)],
  );
});

test("activity records missing a rendered field are rejected, not shown as undefined", () => {
  for (const field of ["displayName", "message", "timestampUtc"]) {
    const partial = { ...record(1) };
    delete partial[field];
    assert.deepEqual(
      loadActivity(memoryStorage({ [ACTIVITY_KEY]: JSON.stringify([partial, record(2)]) })),
      [record(2)],
      field,
    );
  }
});

test("an over-long stored activity list is still capped on read", () => {
  const oversized = JSON.stringify(Array.from({ length: 120 }, (_, i) => record(i)));
  assert.equal(loadActivity(memoryStorage({ [ACTIVITY_KEY]: oversized })).length, ACTIVITY_LIMIT);
});

test("branding round-trips and a broken store reads as nothing", () => {
  const storage = memoryStorage();
  const payload = { title: "Acme", accent: "#0b6e4f" };
  saveBranding(storage, payload);
  assert.deepEqual(JSON.parse(storage.data[BRANDING_KEY]), payload);
  assert.deepEqual(loadBranding(storage), payload);
  assert.equal(loadBranding(memoryStorage({ [BRANDING_KEY]: "{not json" })), null);
  assert.equal(loadBranding(brokenStorage), null);
  saveBranding(brokenStorage, payload);
});
