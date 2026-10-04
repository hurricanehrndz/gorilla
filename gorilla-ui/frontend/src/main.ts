import { api } from "./api.ts";
import {
  ACTIVITY_LIMIT,
  type StorageLike,
  loadActivity,
  loadBranding,
  loadList,
  saveActivity,
  saveBranding,
  saveList,
} from "./cache.ts";
import {
  ALL_CATEGORIES,
  REQUESTED_STATE,
  activityLine,
  addActivity,
  bannerMessage,
  connectionLabel,
  brandingView,
  canCancel,
  cancelTitle,
  cardProgress,
  categories,
  deriveAction,
  fromCache,
  glyphTone,
  httpUrl,
  insertRecord,
  isItemActive,
  isTerminalState,
  localErrorState,
  localRecord,
  monogram,
  myItems,
  progressText,
  refusalText,
  releaseOperation,
  restartBadge,
  shouldAcceptRecord,
  showRetry,
  statusLabel,
  statusRecord,
  stripLine,
  stripView,
  trackOperation,
  visibleItems,
  withFailure,
  withLive,
} from "./state.ts";
import type {
  ActiveOperations,
  ActivityRecord,
  BrandingView,
  ItemAction,
  ListView,
  OperationView,
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

const connection = need<HTMLParagraphElement>("#connection");
const connectionText = need<HTMLSpanElement>("#connection-text");
const retryButton = need<HTMLButtonElement>("#retry");
const navSoftware = need<HTMLButtonElement>("#nav-software");
const navMine = need<HTMLButtonElement>("#nav-mine");
const navActivity = need<HTMLButtonElement>("#nav-activity");
const navBusy = need<SVGElement>("#nav-busy");
const viewHome = need<HTMLElement>("#view-home");
const homeHeading = need<HTMLHeadingElement>("#home-heading");
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
const strip = need<HTMLElement>("#operation-strip");
const stripGlyph = need<HTMLSpanElement>("#strip-glyph");
const stripBar = need<HTMLProgressElement>("#strip-progress");
const stripCancel = need<HTMLButtonElement>("#strip-cancel");
const spinnerTemplate = need<HTMLTemplateElement>("#spinner");

/** View is a page the nav can show; the detail page is drilled into from a list. */
type View = "software" | "mine" | "activity";

let view: ListView = { items: [], source: "loading", savedAtUtc: "" };
let lastError = "";
let dialogOpener: HTMLElement | null = null;
// The list the detail page was opened from, and which Back returns to.
let currentView: View = "software";
// The item shown on the detail page, or null when the list or Activity is up.
let detailItemName: string | null = null;
let activity: ActivityRecord[] = loadActivity(storage);
let active: ActiveOperations = new Map();
// Operations started in this session, oldest first; Activity keeps the history.
const operations = new Map<string, OperationView>();
let localOperations = 0;

const brandBanner = need<HTMLElement>("#banner");
const bannerLogo = need<HTMLSpanElement>("#banner-logo");
const bannerHelp = need<HTMLButtonElement>("#banner-help");
const productMark = need<HTMLSpanElement>("#product-mark");
let branding: BrandingView = brandingView(null);

function logoImage(src: string): HTMLImageElement {
  const img = document.createElement("img");
  img.alt = "";
  img.src = src;
  return img;
}

/**
 * applyBranding shows the organisation branding: admin configuration from the
 * service (policy registry or config.yaml), never catalog data. With nothing
 * configured it leaves the shell exactly as the default markup draws it.
 */
function applyBranding(next: BrandingView): void {
  branding = next;
  brandBanner.hidden = !next.showBanner;
  bannerLogo.hidden = !next.logoSrc;
  bannerLogo.replaceChildren(...(next.logoSrc ? [logoImage(next.logoSrc)] : []));
  need<HTMLParagraphElement>("#banner-title").textContent = next.title;
  need<HTMLParagraphElement>("#banner-tagline").textContent = next.tagline;
  bannerHelp.hidden = !next.helpUrl;
  bannerHelp.textContent = next.helpLabel;
  need<HTMLSpanElement>("#product-name").textContent = next.productName;
  if (next.logoSrc) {
    productMark.replaceChildren(logoImage(next.logoSrc));
  } else {
    productMark.textContent = next.productMark;
  }
  const root = document.documentElement;
  if (next.accent) {
    root.style.setProperty("--brand", next.accent);
    root.style.setProperty("--on-brand", next.onAccent);
    root.dataset.accent = "";
  } else {
    root.style.removeProperty("--brand");
    root.style.removeProperty("--on-brand");
    delete root.dataset.accent;
  }
}

/** refreshBranding converges on the service's branding; a failure keeps the cached one. */
async function refreshBranding(): Promise<void> {
  try {
    const payload = await api.getBranding();
    saveBranding(storage, payload);
    applyBranding(brandingView(payload));
  } catch {
    // An older service or a stopped one: the list refresh reports the connection.
  }
}

function renderBanner(): void {
  const label = connectionLabel(view);
  const detail = bannerMessage(view, lastError);
  connectionText.textContent = label;
  // The indicator stays a short right-aligned label; the sentence with the
  // reason is the tooltip and the accessible name.
  connection.title = detail === label ? "" : detail;
  connection.setAttribute("aria-label", detail);
  connection.dataset.source = view.source;
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

/** setGlyph writes an item's monogram and its stable colour tone. */
function setGlyph(glyph: HTMLElement, item: OptionalInstallItem): void {
  glyph.textContent = monogram(item);
  glyph.dataset.tone = String(glyphTone(item));
}

/** statusLine is a status paragraph: a spinner shown while work runs, then the text. */
function statusLine(): HTMLParagraphElement {
  const status = document.createElement("p");
  status.className = "card-status";
  status.setAttribute("aria-live", "polite");
  const spinner = spinnerTemplate.content.firstElementChild?.cloneNode(true);
  const text = document.createElement("span");
  text.className = "status-text";
  if (spinner) {
    status.append(spinner);
  }
  status.append(text);
  return status;
}

function card(item: OptionalInstallItem): HTMLElement {
  const article = document.createElement("article");
  article.className = "card";

  const glyph = document.createElement("span");
  glyph.className = "glyph";
  glyph.setAttribute("aria-hidden", "true");
  setGlyph(glyph, item);

  const name = document.createElement("h3");
  name.textContent = item.displayName;

  const meta = document.createElement("p");
  meta.className = "card-meta";
  meta.textContent = [item.category, item.developer].filter(Boolean).join(" · ");
  meta.hidden = meta.textContent === "";

  // The status line is the card's one live region: progress and outcome
  // updates are written into it in place (see updateCardProgress), so a screen
  // reader hears "Installing… 50%" without the whole grid being re-read.
  const status = statusLine();

  const actions = document.createElement("p");
  actions.className = "card-actions";

  const action = document.createElement("button");
  const derived = deriveAction(item);
  action.type = "button";
  // Board B draws Remove outlined; the other primary actions are solid.
  action.className = `pill ${derived.label === "Remove" ? "pill-outline" : "pill-primary"}`;
  action.textContent = derived.label;
  // Only the item that owns an in-flight operation is disabled; other items
  // stay actionable and route independently by operation ID.
  action.disabled = isItemActive(active, item.itemName);
  action.setAttribute("aria-label", `${derived.label} ${item.displayName}`);
  action.addEventListener("click", () => void runAction(item, derived));

  const details = document.createElement("button");
  details.type = "button";
  details.className = "pill pill-outline";
  details.textContent = "Details";
  details.setAttribute("aria-label", `Details for ${item.displayName}`);
  details.addEventListener("click", () => openDetail(item, details));

  actions.append(action, details);
  article.append(glyph, name, meta, status, actions);
  article.dataset.item = item.itemName;
  updateCardProgress(article, item, statusLabel(item));
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
 * updateCardProgress writes the item's operation state into its status line:
 * idleText when nothing is going on (an empty one hides the line), otherwise a
 * spinner and one line while the operation runs and the outcome afterwards.
 * The bar is only in the bottom strip and the full timeline only in Activity.
 */
function updateCardProgress(container: HTMLElement, item: OptionalInstallItem, idleText: string): void {
  const status = container.querySelector<HTMLParagraphElement>(".card-status");
  const text = status?.querySelector<HTMLSpanElement>(".status-text");
  const spinner = status?.querySelector<SVGElement>(".spinner");
  if (!status || !text || !spinner) {
    throw new Error("Gorilla UI card is missing its status line");
  }
  const operation = latestOperation(item.itemName);
  const progress = operation
    ? cardProgress(item, operation.records, operation.outcome, operation.action.method, operation.notice)
    : null;
  if (!progress) {
    text.textContent = idleText;
    status.hidden = idleText === "";
    delete status.dataset.outcome;
    spinner.toggleAttribute("hidden", true);
    return;
  }
  text.textContent = progressText(progress);
  status.hidden = false;
  // "plain" opts out of the warning box the stylesheet gives finished outcomes.
  status.dataset.outcome = progress.plain ? "plain" : progress.outcome;
  spinner.toggleAttribute("hidden", progress.outcome !== "active");
}

/** renderCardFor refreshes one item's card in place, keeping the rest of the grid untouched. */
function renderCardFor(itemName: string): void {
  const article = grid.querySelector<HTMLElement>(`[data-item="${CSS.escape(itemName)}"]`);
  const item = view.items.find((candidate) => candidate.itemName === itemName);
  if (article && item) {
    updateCardProgress(article, item, statusLabel(item));
  }
  if (item && detailItemName === itemName) {
    updateCardProgress(viewDetail, item, "");
  }
}

function renderGrid(): void {
  const mine = currentView === "mine";
  const listed = mine ? myItems(view.items) : view.items;
  const shown = visibleItems(listed, searchInput.value, categorySelect.value);
  homeHeading.textContent = mine ? "My items" : "Available software";
  grid.replaceChildren(...shown.map(card));
  if (listed.length === 0) {
    resultStatus.textContent =
      view.source === "loading"
        ? ""
        : mine
          ? "Nothing here is installed or managed yet."
          : "No software is available yet.";
    return;
  }
  resultStatus.textContent =
    shown.length === listed.length
      ? `Showing all ${listed.length} items.`
      : `Showing ${shown.length} of ${listed.length} items.`;
}

/**
 * renderStrip shows the one operation being worked on, with the only progress
 * bar in the UI: determinate when the latest record carries a percentage,
 * indeterminate (never a fake 0%) otherwise.
 */
function renderStrip(): void {
  navBusy.toggleAttribute("hidden", active.size === 0);
  const shown = stripView(operations.values());
  strip.hidden = !shown;
  if (!shown) {
    return;
  }
  const { item } = shown.operation;
  setGlyph(stripGlyph, item);
  need<HTMLSpanElement>("#strip-name").textContent = item.displayName;
  need<HTMLSpanElement>("#strip-text").textContent = stripLine(shown);
  if (typeof shown.progress.percent === "number") {
    stripBar.value = shown.progress.percent;
  } else {
    stripBar.removeAttribute("value");
  }
  stripBar.setAttribute("aria-label", `${shown.progress.label} ${item.displayName}`);
  // Cancel stays visible, but is a real disabled button once the installer has
  // started; its title says why.
  const latest = shown.operation.records.at(-1)?.state ?? "";
  const pending = Boolean(shown.operation.cancelPending);
  stripCancel.disabled = pending || !canCancel(latest);
  stripCancel.title = cancelTitle(latest, pending);
  const what = shown.operation.action.method === "RemoveItem" ? "removal" : "install";
  stripCancel.setAttribute("aria-label", `Cancel ${what} of ${item.displayName}`);
  stripCancel.dataset.operationId = shown.operation.operationId;
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
  renderStrip();
}

/** pushRecord files a record in its operation and in Activity; false for a duplicate. */
function pushRecord(operation: OperationView, record: ActivityRecord): boolean {
  if (!insertRecord(operation.records, record)) {
    return false;
  }
  activity = addActivity(activity, record, ACTIVITY_LIMIT);
  saveActivity(storage, activity);
  if (!viewActivity.hidden) {
    renderActivity();
  }
  return true;
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

/**
 * cancelOperation asks the service to cancel. An accepted cancel ends the
 * operation through its Canceled record on the status channel; a refusal
 * leaves the item's state alone and shows the service's reason on its card.
 */
async function cancelOperation(operation: OperationView): Promise<void> {
  operation.cancelPending = true;
  renderStrip();
  try {
    await api.cancelOperation(operation.operationId);
  } catch (error) {
    operation.cancelPending = false;
    operation.notice = refusalText(reason(error));
    renderCardFor(operation.item.itemName);
    renderStrip();
  }
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

// Wails emits each event on its own goroutine and never serialises them, so
// records can arrive out of order. pushRecord files each by the seq the service
// gave it: a record that arrives after the terminal one lands before it, and
// the finished operation keeps its terminal record as its latest.
api.onOperationStatus((status) => {
  const operation = operations.get(status.operationId);
  if (!operation || !shouldAcceptRecord(operation.outcome)) {
    return;
  }
  const record = statusRecord(status);
  if (!pushRecord(operation, record) || operation.outcome === "terminal") {
    return;
  }
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
  renderStrip();
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
  setGlyph(need<HTMLElement>("#detail-glyph"), item);
  setText("#detail-name", item.displayName, item.itemName);
  setText("#detail-developer", item.developer ?? "", "");
  setText("#detail-description", item.description ?? "", "No description is published for this item.");
  setText("#detail-developer-value", item.developer ?? "");
  setText("#detail-version", item.version);
  setText("#detail-category", item.category ?? "");
  setText("#detail-status", statusLabel(item));
  setText("#detail-restart", restartBadge(item), "None");

  const derived = deriveAction(item);
  detailAction.className = `pill pill-large ${derived.label === "Remove" ? "pill-outline" : "pill-primary"}`;
  detailAction.textContent = derived.label;
  detailAction.disabled = isItemActive(active, item.itemName);
  detailAction.setAttribute("aria-label", `${derived.label} ${item.displayName}`);
  detailAction.onclick = () => void runAction(item, derived);

  // The Status row already says the catalog state, so the line only appears
  // for work in progress or an outcome.
  updateCardProgress(viewDetail, item, "");
}

function openDetail(item: OptionalInstallItem, opener: HTMLElement): void {
  dialogOpener = opener;
  detailItemName = item.itemName;
  renderDetail();
  viewHome.hidden = true;
  viewActivity.hidden = true;
  viewDetail.hidden = false;
  for (const link of [navSoftware, navMine, navActivity]) {
    link.setAttribute("aria-current", "false");
  }
  detailBack.focus();
}

/** closeDetail returns to the list and hands focus back to the card it came from. */
function closeDetail(): void {
  if (viewDetail.hidden) {
    return;
  }
  showView(currentView);
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

function showView(next: View): void {
  const changed = next !== currentView;
  currentView = next;
  viewDetail.hidden = true;
  detailItemName = null;
  viewHome.hidden = next === "activity";
  viewActivity.hidden = next !== "activity";
  navSoftware.setAttribute("aria-current", next === "software" ? "page" : "false");
  navMine.setAttribute("aria-current", next === "mine" ? "page" : "false");
  navActivity.setAttribute("aria-current", next === "activity" ? "page" : "false");
  if (next === "activity") {
    renderActivity();
  } else if (changed) {
    // Software and My items share the grid; only switching lists rebuilds it,
    // so Back from the detail page can refocus the card it came from.
    renderGrid();
  }
}

stripCancel.addEventListener("click", () => {
  const operation = operations.get(stripCancel.dataset.operationId ?? "");
  if (operation && operation.outcome === "active") {
    void cancelOperation(operation);
  }
});
bannerHelp.addEventListener("click", () => {
  // The service validated the URL; check again so nothing but http(s) ever
  // leaves the WebView, and open it in the system browser, never in here.
  const url = httpUrl(branding.helpUrl);
  if (url) {
    void api.openExternal(url).catch(() => {});
  }
});
filtersForm.addEventListener("submit", (event) => event.preventDefault());
searchInput.addEventListener("input", renderGrid);
categorySelect.addEventListener("change", renderGrid);
retryButton.addEventListener("click", () => {
  view = { ...view, source: view.items.length ? "cache" : "loading" };
  render();
  void refresh();
});
navSoftware.addEventListener("click", () => showView("software"));
navMine.addEventListener("click", () => showView("mine"));
navActivity.addEventListener("click", () => showView("activity"));
detailBack.addEventListener("click", closeDetail);
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !viewDetail.hidden) {
    event.preventDefault();
    closeDetail();
  } else if (event.key === "Escape" && !viewActivity.hidden) {
    event.preventDefault();
    showView("software");
  } else if (
    // Ctrl+L shows or hides the log, like Managed Software Center's ⌘L; it is
    // claimed here so the WebView does not treat it as address-bar focus.
    event.ctrlKey &&
    !event.altKey &&
    !event.shiftKey &&
    !event.metaKey &&
    event.key.toLowerCase() === "l"
  ) {
    event.preventDefault();
    showView(viewActivity.hidden ? "activity" : "software");
  }
});

// Render whatever is cached before any network work, then converge on the
// authoritative service response.
applyBranding(brandingView(loadBranding(storage)));
view = fromCache(loadList(storage));
render();
showView("software");
void refresh();
void refreshBranding();
