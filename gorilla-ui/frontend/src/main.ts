import { api } from "./api.ts";
import { type StorageLike, loadActivity, loadList, saveList } from "./cache.ts";
import {
  ALL_CATEGORIES,
  bannerMessage,
  categories,
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
import type { ListView, OptionalInstallItem } from "./types.ts";

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

let view: ListView = { items: [], source: "loading", savedAtUtc: "" };
let lastError = "";
let dialogOpener: HTMLElement | null = null;

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

  // ponytail: the primary action is derived and rendered but stays disabled in
  // this phase because no mutation, progress, or Activity timeline exists yet.
  // Phase 4 removes `disabled` and wires the click to the adapter.
  const action = document.createElement("button");
  const derived = deriveAction(item);
  action.type = "button";
  action.textContent = derived.label;
  action.disabled = true;
  action.setAttribute("aria-label", `${derived.label} ${item.displayName}`);

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
  const records = loadActivity(storage);
  activityEmpty.hidden = records.length > 0;
  activityList.replaceChildren(
    ...records.map((record) => {
      const entry = document.createElement("li");
      entry.textContent = `${record.timestampUtc} — ${record.displayName || record.itemName}: ${record.state} ${record.message}`;
      return entry;
    }),
  );
}

function render(): void {
  renderBanner();
  renderCategories();
  renderGrid();
}

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
