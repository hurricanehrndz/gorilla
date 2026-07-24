import type { OptionalInstallItem } from "./api.ts";

// The protocol models stay single-sourced in the generated bindings; only the
// presentation-side shapes live here.
export type { OptionalInstallItem } from "./api.ts";

export type ActionMethod = "InstallItem" | "RemoveItem";

export type ItemAction = {
  label: "Install" | "Remove" | "Cancel";
  method: ActionMethod;
};

/** CachedList is the `gorilla.optional-items.v1` localStorage shape. */
export type CachedList = {
  savedAtUtc: string;
  items: OptionalInstallItem[];
};

/** ActivityRecord is one local display-history entry. */
export type ActivityRecord = {
  operationId: string;
  itemName: string;
  displayName: string;
  state: string;
  message: string;
  timestampUtc: string;
};

/** ListSource is where the currently rendered item list came from. */
export type ListSource = "loading" | "cache" | "live" | "stale";

export type ListView = {
  items: OptionalInstallItem[];
  source: ListSource;
  savedAtUtc: string;
};
