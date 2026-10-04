import type { Branding } from "../bindings/github.com/1dustindavis/gorilla/pkg/branding/models.js";
import type {
  AcceptedOperation,
  OptionalInstallItem,
} from "../bindings/github.com/1dustindavis/gorilla/pkg/service/models.js";

export type { AcceptedOperation, Branding, OptionalInstallItem };

// ponytail: OperationStatus is hand-typed because the committed bindings are
// generated with -noevents, so no generated model exists for the event payload.
// Upgrade path: regenerate bindings with events and re-export that model here.
export type OperationStatus = {
  operationId: string;
  /** Numbers the operation's records from 1 in the order the service recorded them. */
  seq: number;
  /** RFC 3339 UTC with milliseconds, like Date.toISOString. */
  timestampUtc: string;
  itemName: string;
  displayName: string;
  state: string;
  progressPercent: number;
  message: string;
  errorCode?: string;
  errorMessage?: string;
  canceledBy?: string;
  /** The user whose request started the operation, "" when the service could not tell. */
  requestedBy: string;
};

/** GorillaApi is the entire frontend view of the backend. */
export type GorillaApi = {
  listOptionalInstalls(): Promise<OptionalInstallItem[]>;
  installItem(itemName: string): Promise<AcceptedOperation>;
  removeItem(itemName: string): Promise<AcceptedOperation>;
  watchOperation(operationId: string): Promise<void>;
  /** cancelOperation rejects with the service's operation_not_cancelable message when refused. */
  cancelOperation(operationId: string): Promise<void>;
  onOperationStatus(handler: (status: OperationStatus) => void): void;
  getBranding(): Promise<Branding>;
  /** openExternal opens an http(s) URL in the system browser, never in the WebView. */
  openExternal(url: string): Promise<void>;
};

// The implementation is selected by mode in vite.config.ts: development
// resolves to ./mock-api.ts, every other mode to ./wails-api.ts.
export { api } from "gorilla-api-impl";
