import type {
  AcceptedOperation,
  OptionalInstallItem,
} from "../bindings/github.com/1dustindavis/gorilla/pkg/service/models.js";

export type { AcceptedOperation, OptionalInstallItem };

// ponytail: OperationStatus is hand-typed because the committed bindings are
// generated with -noevents, so no generated model exists for the event payload.
// Upgrade path: regenerate bindings with events and re-export that model here.
export type OperationStatus = {
  operationId: string;
  timestampUtc: string;
  itemName: string;
  displayName: string;
  state: string;
  progressPercent: number;
  message: string;
  errorCode?: string;
  errorMessage?: string;
  canceledBy?: string;
};

/** GorillaApi is the entire frontend view of the backend. */
export type GorillaApi = {
  listOptionalInstalls(): Promise<OptionalInstallItem[]>;
  installItem(itemName: string): Promise<AcceptedOperation>;
  removeItem(itemName: string): Promise<AcceptedOperation>;
  watchOperation(operationId: string): Promise<void>;
  onOperationStatus(handler: (status: OperationStatus) => void): void;
};

// The implementation is selected by mode in vite.config.ts: development
// resolves to ./mock-api.ts, every other mode to ./wails-api.ts.
export { api } from "gorilla-api-impl";
