import type { OptionalInstallItem } from "./api.ts";

// The protocol models stay single-sourced in the generated bindings; only the
// presentation-side shapes live here.
export type { OperationStatus, OptionalInstallItem } from "./api.ts";

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

/**
 * ActivityRecord is one local display-history entry. `state` holds a wire state,
 * a local request state, or one of the local error states from state.ts;
 * `progressPercent` is scoped to `itemName` and is absent on local records.
 */
export type ActivityRecord = {
  operationId: string;
  itemName: string;
  displayName: string;
  state: string;
  message: string;
  timestampUtc: string;
  progressPercent?: number;
  /** The wire errorMessage on its own, so an outcome line can quote just it. */
  detail?: string;
};

/** OperationOutcome is where a locally initiated operation has got to. */
export type OperationOutcome = "active" | "terminal" | "error";

/** OperationView is one locally initiated operation and its display timeline. */
export type OperationView = {
  operationId: string;
  item: OptionalInstallItem;
  action: ItemAction;
  records: ActivityRecord[];
  outcome: OperationOutcome;
};

/** ActiveOperations maps an accepted, non-terminal operationId to its item. */
export type ActiveOperations = ReadonlyMap<string, string>;

/** ListSource is where the currently rendered item list came from. */
export type ListSource = "loading" | "cache" | "live" | "stale";

export type ListView = {
  items: OptionalInstallItem[];
  source: ListSource;
  savedAtUtc: string;
};
