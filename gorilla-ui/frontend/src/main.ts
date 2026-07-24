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
  categories,
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
const detail = need<HTMLDialogElement>("#detail");
const detailClose = need<HTMLButtonElement>("#detail-close");
const viewOperations = need<HTMLElement>("#view-operations");
const operationList = need<HTMLDivElement>("#operation-list");

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

  const status = document.createElement("p");
  status.className = "card-status";
  status.textContent = statusLabel(item);

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
  article.append(glyph, name, meta, status, actions);
  return article;
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

function operationCard(operation: OperationView): HTMLElement {
  const article = document.createElement("article");
  article.className = "operation";
  article.dataset.outcome = operation.outcome;

  const heading = document.createElement("h3");
  heading.textContent = `${operation.action.label} ${operation.item.displayName}`;

  const latest = operation.records[operation.records.length - 1];
  const current = document.createElement("p");
  current.className = "operation-current";
  // Only the current line is a live region: the container is replaced wholesale
  // on every event and it also holds the Retry button, so announcing at the
  // container level would re-read every heading and the whole timeline.
  // ponytail: the paragraph is recreated by each render, so announcement relies
  // on the region being re-inserted with its text; hold cards across renders and
  // update this node in place if a screen reader misses updates.
  current.setAttribute("aria-live", "polite");
  current.textContent = activityLine(latest);

  article.append(heading, current);

  if (operation.outcome === "active") {
    // The engine publishes no aggregate measurement, so the overall indicator
    // stays indeterminate: a <progress> with no value attribute.
    const overall = document.createElement("progress");
    overall.setAttribute("aria-label", `${operation.action.label} ${operation.item.displayName} in progress`);
    article.append(overall);

    // A percentage is only meaningful for the item the event names, and it may
    // reset when a dependency or updater takes over.
    if (isItemPhase(latest.state) && typeof latest.progressPercent === "number") {
      const label = document.createElement("p");
      label.className = "operation-item";
      label.textContent = `${progressLabel(latest)} — ${latest.progressPercent}%`;
      const bar = document.createElement("progress");
      bar.max = 100;
      bar.value = latest.progressPercent;
      bar.setAttribute("aria-label", progressLabel(latest));
      article.append(label, bar);
    }
  }

  const timeline = document.createElement("ol");
  timeline.className = "timeline";
  timeline.replaceChildren(
    ...operation.records.map((record) => {
      const entry = document.createElement("li");
      entry.textContent = activityLine(record);
      return entry;
    }),
  );
  article.append(timeline);

  if (operation.outcome === "error") {
    const retry = document.createElement("button");
    retry.type = "button";
    retry.textContent = "Retry";
    retry.setAttribute("aria-label", `Retry ${operation.action.label} ${operation.item.displayName}`);
    retry.addEventListener("click", () => {
      operations.delete(operation.operationId);
      renderOperations();
      void runAction(operation.item, operation.action);
    });
    article.append(retry);
  }

  return article;
}

function renderOperations(): void {
  viewOperations.hidden = operations.size === 0;
  operationList.replaceChildren(...[...operations.values()].map(operationCard));
}

function render(): void {
  renderBanner();
  renderCategories();
  renderGrid();
  renderOperations();
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
    renderOperations();
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
  // Each operation's current line is its own aria-live region, so re-rendering
  // announces the new state, message and item identity without re-reading the
  // whole list.
  renderOperations();
});

function setText(selector: string, value: string, fallback = "Not provided"): void {
  need<HTMLElement>(selector).textContent = value.trim() || fallback;
}

function openDetail(item: OptionalInstallItem, opener: HTMLElement): void {
  dialogOpener = opener;
  need<HTMLElement>("#detail-glyph").textContent = monogram(item);
  setText("#detail-name", item.displayName, item.itemName);
  setText("#detail-status", statusLabel(item));
  setText("#detail-version", item.version);
  setText("#detail-developer", item.developer ?? "");
  setText("#detail-category", item.category ?? "");
  setText("#detail-description", item.description ?? "", "No description is published for this item.");

  const restart = need<HTMLParagraphElement>("#detail-restart");
  const badge = restartBadge(item);
  restart.textContent = badge;
  restart.hidden = badge === "";

  detail.showModal();
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
detailClose.addEventListener("click", () => detail.close());
detail.addEventListener("close", () => {
  dialogOpener?.focus();
  dialogOpener = null;
});

// Render whatever is cached before any network work, then converge on the
// authoritative service response.
view = fromCache(loadList(storage));
render();
showView(false);
void refresh();
