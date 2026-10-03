import type {
  ActiveOperations,
  ActivityRecord,
  CachedList,
  ItemAction,
  ListView,
  OperationOutcome,
  OperationStatus,
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
  return stateLabel(item.status);
}

/** REQUESTED_STATE is local: the service accepted the request, no event yet. */
export const REQUESTED_STATE = "Requested";
/** STREAM_ENDED_STATE is local: the stream ended before any terminal record. */
export const STREAM_ENDED_STATE = "stream_ended";
/** ERROR_STATE is local: the request or watch call itself failed. */
export const ERROR_STATE = "error";

const LOCAL_STATE_LABELS: Record<string, string> = {
  [STREAM_ENDED_STATE]: "Stream ended before a result",
  [ERROR_STATE]: "Request failed",
};

export function stateLabel(state: string): string {
  const raw = state.trim();
  if (!raw) {
    return "Unknown";
  }
  return LOCAL_STATE_LABELS[raw] ?? raw.replace(/([a-z])([A-Z])/g, "$1 $2");
}

// Only these four states end an operation. ItemCompleted and ItemFailed are
// per-item records: a dependency can fail while the operation keeps running.
const TERMINAL_STATES = new Set(["Succeeded", "Failed", "Deferred", "Canceled"]);

// Only these states describe work on the event's own item, so only they carry a
// percentage worth showing on a determinate bar.
const ITEM_PHASE_STATES = new Set(["Downloading", "Installing", "Removing"]);

export function isTerminalState(state: string): boolean {
  return TERMINAL_STATES.has(state.trim());
}

export function isItemPhase(state: string): boolean {
  return ITEM_PHASE_STATES.has(state.trim());
}

/** localErrorState keeps a premature stream end distinguishable from any other failure. */
export function localErrorState(error: string): string {
  return /stream ended before a terminal event/i.test(error) ? STREAM_ENDED_STATE : ERROR_STATE;
}

/** trackOperation records an accepted operation so only its item is disabled. */
export function trackOperation(
  active: ActiveOperations,
  operationId: string,
  itemName: string,
): ActiveOperations {
  return new Map(active).set(operationId, itemName);
}

export function releaseOperation(
  active: ActiveOperations,
  operationId: string,
): ActiveOperations {
  const next = new Map(active);
  next.delete(operationId);
  return next;
}

export function isItemActive(active: ActiveOperations, itemName: string): boolean {
  for (const name of active.values()) {
    if (name === itemName) {
      return true;
    }
  }
  return false;
}

/**
 * shouldAcceptRecord decides whether a newly arrived status record still
 * belongs to an operation. Wails emits every event on its own goroutine and the
 * service flushes a whole poll batch at once, so a non-terminal record can
 * arrive after the terminal one; accepting it would show finished work as still
 * running.
 */
export function shouldAcceptRecord(outcome: OperationOutcome): boolean {
  return outcome === "active";
}

/**
 * statusRecord narrows a wire status event to a local display record. The
 * record is display history only: installed state comes from a later
 * authoritative list, never from a progress event.
 */
export function statusRecord(status: OperationStatus): ActivityRecord {
  const message = status.message.trim();
  const detail = (status.errorMessage ?? "").trim();
  return {
    operationId: status.operationId,
    itemName: status.itemName,
    displayName: status.displayName || status.itemName,
    state: status.state,
    message: [message, detail && detail !== message ? `(${detail})` : ""].filter(Boolean).join(" "),
    timestampUtc: status.timestampUtc,
    progressPercent: status.progressPercent,
  };
}

/** localRecord is a display-only entry for work the service never reported. */
export function localRecord(
  operationId: string,
  item: Pick<OptionalInstallItem, "itemName" | "displayName">,
  state: string,
  message: string,
  timestampUtc: string,
): ActivityRecord {
  return {
    operationId,
    itemName: item.itemName,
    displayName: item.displayName || item.itemName,
    state,
    message,
    timestampUtc,
  };
}

export function activityLine(record: ActivityRecord): string {
  const when = new Date(record.timestampUtc);
  const stamp = Number.isNaN(when.getTime()) ? record.timestampUtc : when.toLocaleString();
  const name = record.displayName || record.itemName;
  return [`${stamp} — ${name}: ${stateLabel(record.state)}`, record.message.trim()]
    .filter(Boolean)
    .join(" — ");
}

/** progressLabel names the item a determinate bar belongs to, never the operation. */
export function progressLabel(record: ActivityRecord): string {
  return `${record.displayName || record.itemName} — ${stateLabel(record.state)}`;
}

/**
 * CardProgress is what an item's own card shows for the operation the user
 * started on it: one short line and a bar while it runs, then only an outcome
 * worth acting on (Failed, Deferred, a request error). The full timeline is in
 * Activity, the way Managed Software Center keeps its log off the main view.
 */
export type CardProgress = {
  label: string;
  /** Determinate percentage; absent while active means an indeterminate bar. */
  percent?: number;
  outcome: OperationOutcome;
};

export function cardProgress(
  item: Pick<OptionalInstallItem, "itemName">,
  records: ActivityRecord[],
  outcome: OperationOutcome,
): CardProgress | null {
  const latest = records[records.length - 1];
  if (!latest) {
    return null;
  }
  if (outcome === "active") {
    const own = latest.itemName === item.itemName;
    if (isItemPhase(latest.state)) {
      // A dependency or updater can take over mid-run; name it so the bar's
      // percentage (which is scoped to that item) is not read as this item's.
      const who = own ? "" : ` ${latest.displayName || latest.itemName}`;
      return {
        label: `${stateLabel(latest.state)}${who}…`,
        percent: typeof latest.progressPercent === "number" ? latest.progressPercent : undefined,
        outcome,
      };
    }
    return { label: own ? `${stateLabel(latest.state)}…` : "In progress…", outcome };
  }
  // A success needs no epilogue: the refreshed list status is authoritative.
  if (outcome === "terminal" && latest.state === "Succeeded") {
    return null;
  }
  const message = latest.message.trim();
  return {
    label: message ? `${stateLabel(latest.state)} — ${message}` : stateLabel(latest.state),
    outcome,
  };
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
