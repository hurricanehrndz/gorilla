import { Events } from "@wailsio/runtime";

import { UIService } from "../bindings/github.com/1dustindavis/gorilla/gorilla-ui/index.js";
import type { GorillaApi, OperationStatus } from "./api.ts";

export const api: GorillaApi = {
  listOptionalInstalls: () => UIService.ListOptionalInstalls(),
  installItem: (itemName) => UIService.InstallItem(itemName),
  removeItem: (itemName) => UIService.RemoveItem(itemName),
  watchOperation: (operationId) => UIService.WatchOperation(operationId),
  onOperationStatus(handler) {
    Events.On("gorilla:operation-status", (event) => {
      handler(event.data as OperationStatus);
    });
  },
};
