import { api } from "./api.ts";
import {
  ACTIVITY_LIMIT,
  type StorageLike,
  loadActivity,
  loadList,
  saveActivity,
  saveList,
} from "./cache.ts";
import {
  ALL_CATEGORIES,
  REQUESTED_STATE,
  activityLine,
  bannerMessage,
  cardProgress,
  categories,
  deriveAction,
  fromCache,
  isItemActive,
  isTerminalState,
  localErrorState,
  localRecord,
  monogram,
  releaseOperation,
  restartBadge,
  shouldAcceptRecord,
  showRetry,
  statusLabel,
  statusRecord,
  trackOperation,
  visibleItems,
  withFailure,
  withLive,
} from "./state.ts";
import type {
  ActiveOperations,
  ActivityRecord,
  ItemAction,
  ListView,
  OperationOutcome,
  OptionalInstallItem,
} from "./types.ts";

// When an origin has storage blocked, reading the `localStorage` property
// itself throws SecurityError, so the try/catch has to wrap the property
// access — cache.ts only guards the getItem/setItem calls.
const storage: StorageLike = (() => {
  try {
    return localStorage;
  } catch {
    return { getItem: () => null, setItem: () => {} };
  }
})();

function need<E extends Element>(selector: string): E {
  const element = document.querySelector<E>(selector);
  if (!element) {
    throw new Error(`Gorilla UI shell is missing ${selector}`);
  }
  return element;
}

const bannerRegion = need<HTMLParagraphElement>("#service-banner");
const bannerText = need<HTMLSpanElement>("#banner-text");
const retryButton = need<HTMLButtonElement>("#retry");
const navHome = need<HTMLButtonElement>("#nav-home");
const navActivity = need<HTMLButtonElement>("#nav-activity");
const viewHome = need<HTMLElement>("#view-home");
const viewActivity = need<HTMLElement>("#view-activity");
const searchInput = need<HTMLInputElement>("#search");
const categorySelect = need<HTMLSelectElement>("#category");
const filtersForm = need<HTMLFormElement>("#filters");
const resultStatus = need<HTMLParagraphElement>("#result-status");
const grid = need<HTMLDivElement>("#item-grid");
const activityList = need<HTMLOListElement>("#activity-list");
const activityEmpty = need<HTMLParagraphElement>("#activity-empty");
const viewDetail = need<HTMLElement>("#view-detail");
const detailBack = need<HTMLButtonElement>("#detail-back");
const detailAction = need<HTMLButtonElement>("#detail-action");

/** OperationView is one locally initiated operation and its display timeline. */
type OperationView = {
  operationId: string;
  item: OptionalInstallItem;
  action: ItemAction;
  records: ActivityRecord[];
  outcome: OperationOutcome;
};

let view: ListView = { items: [], source: "loading", savedAtUtc: "" };
let lastError = "";
let dialogOpener: HTMLElement | null = null;
// The item shown on the detail page, or null when the list or Activity is up.
let detailItemName: string | null = null;
let activity: ActivityRecord[] = loadActivity(storage);
let active: ActiveOperations = new Map();
// Operations started in this session, oldest first; Activity keeps the history.
const operations = new Map<string, OperationView>();
let localOperations = 0;

function renderBanner(): void {
  bannerText.textContent = bannerMessage(view, lastError);
  bannerRegion.dataset.source = view.source;
  retryButton.hidden = !showRetry(view);
}

function renderCategories(): void {
  const selected = categorySelect.value;
  categorySelect.replaceChildren();
  const all = document.createElement("option");
  all.value = ALL_CATEGORIES;
  all.textContent = "All categories";
  categorySelect.append(all);
  for (const category of categories(view.items)) {
    const option = document.createElement("option");
    option.value = category;
    option.textContent = category;
    categorySelect.append(option);
  }
  // Assigning an option value that no longer exists clears the select, which is
  // exactly the fallback we want.
  categorySelect.value = selected;
  if (!categorySelect.value) {
    categorySelect.value = ALL_CATEGORIES;
  }
}

function card(item: OptionalInstallItem): HTMLElement {
  const article = document.createElement("article");
  article.className = "card";

  const glyph = document.createElement("span");
  glyph.className = "glyph";
  glyph.setAttribute("aria-hidden", "true");
  glyph.textContent = monogram(item);

  const name = document.createElement("h3");
  name.textContent = item.displayName;

  const meta = document.createElement("p");
  meta.className = "card-meta";
  meta.textContent = [item.version, item.developer, item.category].filter(Boolean).join(" · ");

  // The status line is the card's one live region: progress and outcome
  // updates are written into it in place (see updateCardProgress), so a screen
  // reader hears "Installing… 50%" without the whole grid being re-read.
  const status = document.createElement("p");
  status.className = "card-status";
  status.setAttribute("aria-live", "polite");

  const bar = document.createElement("progress");
  bar.className = "card-progress";
  bar.max = 100;
  bar.hidden = true;

  const actions = document.createElement("p");
  actions.className = "card-actions";

  const action = document.createElement("button");
  const derived = deriveAction(item);
  action.type = "button";
  action.textContent = derived.label;
  // Only the item that owns an in-flight operation is disabled; other items
  // stay actionable and route independently by operation ID.
  action.disabled = isItemActive(active, item.itemName);
  action.setAttribute("aria-label", `${derived.label} ${item.displayName}`);
  action.addEventListener("click", () => void runAction(item, derived));

  const details = document.createElement("button");
  details.type = "button";
  details.textContent = "Details";
  details.setAttribute("aria-label", `Details for ${item.displayName}`);
  details.addEventListener("click", () => openDetail(item, details));

  actions.append(action, details);
  article.append(glyph, name, meta, status, bar, actions);
  article.dataset.item = item.itemName;
  updateCardProgress(article, item);
  return article;
}

/** latestOperation is the most recently started operation on an item, if any. */
function latestOperation(itemName: string): OperationView | undefined {
  let found: OperationView | undefined;
  for (const operation of operations.values()) {
    if (operation.item.itemName === itemName) {
      found = operation;
    }
  }
  return found;
}

/**
 * updateCardProgress writes the item's operation state into its card: the
 * catalog status when nothing is going on, otherwise one line plus a bar while
 * the operation runs and the outcome afterwards. The full timeline is only in
 * Activity, the way Managed Software Center keeps its log off the main view.
 */
function updateCardProgress(article: HTMLElement, item: OptionalInstallItem): void {
  const status = article.querySelector<HTMLParagraphElement>(".card-status");
  const bar = article.querySelector<HTMLProgressElement>(".card-progress");
  if (!status || !bar) {
    throw new Error("Gorilla UI card is missing its status line");
  }
  const operation = latestOperation(item.itemName);
  const progress = operation ? cardProgress(item, operation.records, operation.outcome) : null;
  if (!progress) {
    status.textContent = statusLabel(item);
    delete status.dataset.outcome;
    bar.hidden = true;
    return;
  }
  const percent = typeof progress.percent === "number" ? ` ${progress.percent}%` : "";
  status.textContent = `${progress.label}${percent}`;
  status.dataset.outcome = progress.outcome;
  bar.hidden = progress.outcome !== "active";
  // No percentage means the engine has not measured this phase: an
  // indeterminate bar, never a fake 0%.
  if (typeof progress.percent === "number") {
    bar.value = progress.percent;
  } else {
    bar.removeAttribute("value");
  }
  bar.setAttribute("aria-label", `${progress.label} ${item.displayName}`);
}

/** renderCardFor refreshes one item's card in place, keeping the rest of the grid untouched. */
function renderCardFor(itemName: string): void {
  const article = grid.querySelector<HTMLElement>(`[data-item="${CSS.escape(itemName)}"]`);
  const item = view.items.find((candidate) => candidate.itemName === itemName);
  if (article && item) {
    updateCardProgress(article, item);
  }
  if (item && detailItemName === itemName) {
    updateCardProgress(viewDetail, item);
  }
}

function renderGrid(): void {
  const shown = visibleItems(view.items, searchInput.value, categorySelect.value);
  grid.replaceChildren(...shown.map(card));
  if (view.items.length === 0) {
    resultStatus.textContent = view.source === "loading" ? "" : "No software is available yet.";
    return;
  }
  resultStatus.textContent =
    shown.length === view.items.length
      ? `Showing all ${view.items.length} items.`
      : `Showing ${shown.length} of ${view.items.length} items.`;
}

function renderActivity(): void {
  activityEmpty.hidden = activity.length > 0;
  activityList.replaceChildren(
    ...activity.map((record) => {
      const entry = document.createElement("li");
      entry.textContent = activityLine(record);
      return entry;
    }),
  );
}

function render(): void {
  renderBanner();
  renderCategories();
  renderGrid();
  renderDetail();
}

function pushRecord(operation: OperationView, record: ActivityRecord): void {
  operation.records.push(record);
  activity = [record, ...activity].slice(0, ACTIVITY_LIMIT);
  saveActivity(storage, activity);
  if (!viewActivity.hidden) {
    renderActivity();
  }
}

function reason(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function runAction(item: OptionalInstallItem, action: ItemAction): Promise<void> {
  const started = new Date().toISOString();
  let operationId = "";
  try {
    const accepted =
      action.method === "InstallItem"
        ? await api.installItem(item.itemName)
        : await api.removeItem(item.itemName);
    operationId = (accepted.operationId ?? "").trim();
    if (!accepted.accepted || !operationId) {
      throw new Error("the service did not accept the request");
    }
  } catch (error) {
    localOperations += 1;
    const failed: OperationView = {
      operationId: `local-${localOperations}`,
      item,
      action,
      records: [],
      outcome: "error",
    };
    operations.set(failed.operationId, failed);
    pushRecord(
      failed,
      localRecord(failed.operationId, item, localErrorState(reason(error)), reason(error), started),
    );
    renderCardFor(item.itemName);
    return;
  }

  const operation: OperationView = { operationId, item, action, records: [], outcome: "active" };
  operations.set(operationId, operation);
  pushRecord(
    operation,
    localRecord(operationId, item, REQUESTED_STATE, `${action.label} accepted by the service.`, started),
  );
  active = trackOperation(active, operationId, item.itemName);
  render();

  // Watching must not block the UI: status arrives on the shared event channel
  // and only failures come back through this promise.
  void api.watchOperation(operationId).catch((error) => failOperation(operationId, reason(error)));
}

/** failOperation marks only the local display record; it never touches item state. */
function failOperation(operationId: string, message: string): void {
  const operation = operations.get(operationId);
  if (!operation || operation.outcome !== "active") {
    return;
  }
  operation.outcome = "error";
  active = releaseOperation(active, operationId);
  pushRecord(
    operation,
    localRecord(operationId, operation.item, localErrorState(message), message, new Date().toISOString()),
  );
  render();
}

// ponytail: timeline records are kept in arrival order. Wails emits each event
// on its own goroutine and never serialises them, so records inside one poll
// batch can arrive out of order. Anything arriving after the operation reached a
// terminal or error outcome is dropped, which is what keeps a finished operation
// from reverting to a progress line; an inversion between two non-terminal
// records is cosmetic and tolerated. Sorting by timestampUtc is not an option
// until the wire carries sub-second precision — it is second-granular today, so
// it cannot break ties inside a 20ms batch.
api.onOperationStatus((status) => {
  const operation = operations.get(status.operationId);
  if (!operation || !shouldAcceptRecord(operation.outcome)) {
    return;
  }
  const record = statusRecord(status);
  pushRecord(operation, record);
  // ItemCompleted and ItemFailed are per-item records; only the four terminal
  // states end the operation, and only an authoritative list changes the cards.
  if (isTerminalState(record.state)) {
    operation.outcome = "terminal";
    active = releaseOperation(active, status.operationId);
    render();
    void refresh();
    return;
  }
  // The item's card status line is its own aria-live region, so updating it in
  // place announces the new state without re-reading the whole grid.
  renderCardFor(operation.item.itemName);
});

function setText(selector: string, value: string, fallback = "Not provided"): void {
  need<HTMLElement>(selector).textContent = value.trim() || fallback;
}

/**
 * renderDetail fills the drill-down page for the item being viewed. It reads
 * the item from the current list, so a refresh after a terminal record updates
 * the status and the action button in place.
 */
function renderDetail(): void {
  const item = detailItemName ? view.items.find((candidate) => candidate.itemName === detailItemName) : undefined;
  if (!item) {
    return;
  }
  need<HTMLElement>("#detail-glyph").textContent = monogram(item);
  setText("#detail-name", item.displayName, item.itemName);
  setText("#detail-developer", item.developer ?? "", "");
  setText("#detail-version", item.version);
  setText("#detail-developer-value", item.developer ?? "");
  setText("#detail-category", item.category ?? "");
  setText("#detail-description", item.description ?? "", "No description is published for this item.");

  const restart = need<HTMLParagraphElement>("#detail-restart");
  const badge = restartBadge(item);
  restart.textContent = badge;
  restart.hidden = badge === "";

  const derived = deriveAction(item);
  detailAction.textContent = derived.label;
  detailAction.disabled = isItemActive(active, item.itemName);
  detailAction.setAttribute("aria-label", `${derived.label} ${item.displayName}`);
  detailAction.onclick = () => void runAction(item, derived);

  updateCardProgress(viewDetail, item);
}

function openDetail(item: OptionalInstallItem, opener: HTMLElement): void {
  dialogOpener = opener;
  detailItemName = item.itemName;
  renderDetail();
  viewHome.hidden = true;
  viewActivity.hidden = true;
  viewDetail.hidden = false;
  navHome.setAttribute("aria-current", "false");
  navActivity.setAttribute("aria-current", "false");
  detailBack.focus();
}

/** closeDetail returns to the list and hands focus back to the card it came from. */
function closeDetail(): void {
  if (viewDetail.hidden) {
    return;
  }
  detailItemName = null;
  showView(false);
  dialogOpener?.focus();
  dialogOpener = null;
}

async function refresh(): Promise<void> {
  try {
    const items = await api.listOptionalInstalls();
    const savedAtUtc = new Date().toISOString();
    view = withLive(items, savedAtUtc);
    lastError = "";
    saveList(storage, items, savedAtUtc);
  } catch (error) {
    lastError = error instanceof Error ? error.message : String(error);
    view = withFailure(view);
  }
  render();
}

function showView(activity: boolean): void {
  viewDetail.hidden = true;
  detailItemName = null;
  viewHome.hidden = activity;
  viewActivity.hidden = !activity;
  navHome.setAttribute("aria-current", activity ? "false" : "page");
  navActivity.setAttribute("aria-current", activity ? "page" : "false");
  if (activity) {
    renderActivity();
  }
}

filtersForm.addEventListener("submit", (event) => event.preventDefault());
searchInput.addEventListener("input", renderGrid);
categorySelect.addEventListener("change", renderGrid);
retryButton.addEventListener("click", () => {
  view = { ...view, source: view.items.length ? "cache" : "loading" };
  render();
  void refresh();
});
navHome.addEventListener("click", () => showView(false));
navActivity.addEventListener("click", () => showView(true));
detailBack.addEventListener("click", closeDetail);
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !viewDetail.hidden) {
    event.preventDefault();
    closeDetail();
  }
});

// Render whatever is cached before any network work, then converge on the
// authoritative service response.
view = fromCache(loadList(storage));
render();
showView(false);
void refresh();
