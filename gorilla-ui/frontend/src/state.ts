import type {
  CachedList,
  ItemAction,
  ListView,
  OptionalInstallItem,
} from "./types.ts";

export const ALL_CATEGORIES = "all";

const RESTART_LABELS: Record<string, string> = {
  requirerestart: "Restart required",
  recommendrestart: "Restart recommended",
  requirelogout: "Sign out required",
  requireshutdown: "Shut down required",
};

// Category glyphs are generated locally; the specification forbids fetching
// remote catalog icons.
const CATEGORY_GLYPHS: Record<string, string> = {
  browsers: "🌐",
  communication: "💬",
  development: "⌨",
  media: "🎬",
  productivity: "📄",
  security: "🛡",
  utilities: "🔧",
};

const GENERIC_GLYPH = "▪";

function fold(value: string): string {
  return value.trim().toLowerCase();
}

function matchesSearch(item: OptionalInstallItem, query: string): boolean {
  const needle = fold(query);
  if (!needle) {
    return true;
  }
  return [item.displayName, item.itemName, item.developer ?? "", item.category ?? ""].some(
    (field) => field.toLowerCase().includes(needle),
  );
}

function matchesCategory(item: OptionalInstallItem, category: string): boolean {
  return category === ALL_CATEGORIES || fold(item.category ?? "") === fold(category);
}

/** compareItems gives a stable display order: display name, then item name. */
export function compareItems(a: OptionalInstallItem, b: OptionalInstallItem): number {
  return (
    a.displayName.localeCompare(b.displayName, "en", { sensitivity: "base" }) ||
    a.itemName.localeCompare(b.itemName, "en", { sensitivity: "base" })
  );
}

export function visibleItems(
  items: OptionalInstallItem[],
  query: string,
  category: string,
): OptionalInstallItem[] {
  return items
    .filter((item) => matchesSearch(item, query) && matchesCategory(item, category))
    .sort(compareItems);
}

export function categories(items: OptionalInstallItem[]): string[] {
  const seen = new Map<string, string>();
  for (const item of items) {
    const category = (item.category ?? "").trim();
    if (category && !seen.has(fold(category))) {
      seen.set(fold(category), category);
    }
  }
  return [...seen.values()].sort((a, b) => a.localeCompare(b, "en", { sensitivity: "base" }));
}

export function categoryGlyph(category: string | undefined): string {
  return CATEGORY_GLYPHS[fold(category ?? "")] ?? GENERIC_GLYPH;
}

/** monogram returns up to two initials, falling back to a category glyph. */
export function monogram(item: OptionalInstallItem): string {
  const words = (item.displayName || item.itemName).split(/[^\p{L}\p{N}]+/u).filter(Boolean);
  const initials = words
    .slice(0, 2)
    .map((word) => word[0]?.toUpperCase() ?? "")
    .join("");
  return initials || categoryGlyph(item.category);
}

/** restartBadge returns "" unless restartAction is meaningful. */
export function restartBadge(item: OptionalInstallItem): string {
  const raw = (item.restartAction ?? "").trim();
  if (!raw || fold(raw) === "none") {
    return "";
  }
  return RESTART_LABELS[fold(raw)] ?? raw;
}

export function statusLabel(item: OptionalInstallItem): string {
  const raw = item.status.trim();
  return raw ? raw.replace(/([a-z])([A-Z])/g, "$1 $2") : "Unknown";
}

/**
 * deriveAction maps an item to its single primary action. Cancelling a pending
 * install or removal reuses the opposite mutation; it is not a new protocol
 * operation.
 */
export function deriveAction(item: OptionalInstallItem): ItemAction {
  switch (item.status) {
    case "WillBeInstalled":
      return { label: "Cancel", method: "RemoveItem" };
    case "WillBeRemoved":
      return { label: "Cancel", method: "InstallItem" };
    default:
      return item.isManaged
        ? { label: "Remove", method: "RemoveItem" }
        : { label: "Install", method: "InstallItem" };
  }
}

export function fromCache(cached: CachedList | null): ListView {
  if (!cached) {
    return { items: [], source: "loading", savedAtUtc: "" };
  }
  return { items: cached.items, source: "cache", savedAtUtc: cached.savedAtUtc };
}

export function withLive(items: OptionalInstallItem[], nowUtc: string): ListView {
  return { items, source: "live", savedAtUtc: nowUtc };
}

/** withFailure keeps whatever is already rendered and marks it stale. */
export function withFailure(view: ListView): ListView {
  return { ...view, source: "stale" };
}

export function bannerMessage(view: ListView, error: string): string {
  switch (view.source) {
    case "loading":
      return "Connecting to the Gorilla service…";
    case "cache":
      return "Showing cached software. Refreshing…";
    case "live":
      return "Connected to the Gorilla service.";
    case "stale":
      return (
        (view.items.length
          ? `Service unavailable — showing cached software${formatSavedAt(view.savedAtUtc)}.`
          : "Service unavailable and no cached software is stored.") + formatReason(error)
      );
  }
}

export function showRetry(view: ListView): boolean {
  return view.source === "stale";
}

/** formatReason labels the underlying error so it does not read as a sentence fragment. */
function formatReason(error: string): string {
  const raw = error.trim();
  return raw ? ` Reason: ${raw}` : "";
}

function formatSavedAt(savedAtUtc: string): string {
  const saved = new Date(savedAtUtc);
  return Number.isNaN(saved.getTime()) ? "" : ` from ${saved.toLocaleString()}`;
}
