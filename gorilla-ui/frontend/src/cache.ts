import type { ActivityRecord, CachedList, OptionalInstallItem } from "./types.ts";

export const LIST_KEY = "gorilla.optional-items.v1";
export const ACTIVITY_KEY = "gorilla.activity.v1";
export const ACTIVITY_LIMIT = 100;

/** StorageLike is the localStorage subset the cache needs. */
export type StorageLike = Pick<Storage, "getItem" | "setItem">;

// Corrupt or unavailable storage must never break a live request, so every
// entry point swallows storage and parse failures.
function read(storage: StorageLike, key: string): unknown {
  try {
    const raw = storage.getItem(key);
    return raw === null ? null : JSON.parse(raw);
  } catch {
    return null;
  }
}

function write(storage: StorageLike, key: string, value: unknown): void {
  try {
    storage.setItem(key, JSON.stringify(value));
  } catch {
    // Quota, private mode, or a disabled store: the cache is an optimisation.
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isItem(value: unknown): value is OptionalInstallItem {
  return (
    isRecord(value) &&
    typeof value.itemName === "string" &&
    typeof value.displayName === "string" &&
    typeof value.status === "string"
  );
}

function isActivityRecord(value: unknown): value is ActivityRecord {
  return (
    isRecord(value) &&
    typeof value.operationId === "string" &&
    typeof value.itemName === "string" &&
    typeof value.displayName === "string" &&
    typeof value.state === "string" &&
    typeof value.message === "string" &&
    typeof value.timestampUtc === "string"
  );
}

export function loadList(storage: StorageLike): CachedList | null {
  const parsed = read(storage, LIST_KEY);
  if (
    !isRecord(parsed) ||
    typeof parsed.savedAtUtc !== "string" ||
    !Array.isArray(parsed.items) ||
    !parsed.items.every(isItem)
  ) {
    return null;
  }
  return { savedAtUtc: parsed.savedAtUtc, items: parsed.items };
}

export function saveList(
  storage: StorageLike,
  items: OptionalInstallItem[],
  savedAtUtc: string,
): void {
  write(storage, LIST_KEY, { savedAtUtc, items } satisfies CachedList);
}

/** loadActivity reads newest-first records; callers must keep that order. */
export function loadActivity(storage: StorageLike): ActivityRecord[] {
  const parsed = read(storage, ACTIVITY_KEY);
  if (!Array.isArray(parsed)) {
    return [];
  }
  return parsed.filter(isActivityRecord).slice(0, ACTIVITY_LIMIT);
}

/** saveActivity persists the newest records first, capped at ACTIVITY_LIMIT. */
export function saveActivity(storage: StorageLike, records: ActivityRecord[]): void {
  write(storage, ACTIVITY_KEY, records.slice(0, ACTIVITY_LIMIT));
}
