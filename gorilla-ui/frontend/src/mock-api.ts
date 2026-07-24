// GORILLA_VITE_MOCK_ONLY — this module exists so `npm run dev` can render the
// UI in a plain browser. vite.config.ts resolves it only in development mode,
// and the production-bundle gate greps for this marker.
import type {
  AcceptedOperation,
  GorillaApi,
  OperationStatus,
  OptionalInstallItem,
} from "./api.ts";

const MOCK_MARKER = "GORILLA_VITE_MOCK_ONLY";

const items: OptionalInstallItem[] = [
  {
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    version: "1.2.3",
    catalog: "selfserve_catalog",
    description: "A deterministic self-service package used for browser work.",
    category: "Utilities",
    developer: "Gorilla",
    restartAction: "RecommendRestart",
    isManaged: false,
    isInstalled: false,
    status: "NotInstalled",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
  {
    itemName: "DemoUpdater",
    displayName: "Demo Updater",
    version: "4.0.0",
    catalog: "selfserve_catalog",
    description: "A dependency of Demo Optional, installed as part of its run.",
    category: "Utilities",
    developer: "Gorilla",
    isManaged: true,
    isInstalled: true,
    status: "Installed",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
  {
    itemName: "DemoFailing",
    displayName: "Demo Failing",
    version: "0.9.0",
    catalog: "selfserve_catalog",
    description: "Always fails during installation, proving honest terminal state.",
    category: "Development",
    developer: "Gorilla",
    restartAction: "none",
    isManaged: false,
    isInstalled: false,
    status: "NotInstalled",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
  {
    itemName: "DemoBlocked",
    displayName: "Demo Blocked",
    version: "2.0.0",
    catalog: "selfserve_catalog",
    description: "Deferred while its blocking application is running.",
    category: "Productivity",
    developer: "Gorilla",
    restartAction: "RequireRestart",
    isManaged: false,
    isInstalled: false,
    status: "NotInstalled",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
  {
    itemName: "DemoPending",
    displayName: "Demo Pending",
    version: "3.1.0",
    catalog: "selfserve_catalog",
    description: "Already requested; its primary action is Cancel.",
    category: "Security",
    developer: "Gorilla",
    isManaged: true,
    isInstalled: false,
    status: "WillBeInstalled",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
  {
    itemName: "DemoNoIcon",
    displayName: "7",
    version: "7.0.0",
    catalog: "selfserve_catalog",
    description: "Exercises the monogram fallback for a numeric display name.",
    isManaged: false,
    isInstalled: false,
    status: "Unknown",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
];

const handlers: ((status: OperationStatus) => void)[] = [];
let operationCounter = 0;

function offline(): boolean {
  return globalThis.location?.search.includes("offline") ?? false;
}

function accept(): AcceptedOperation {
  operationCounter += 1;
  return {
    operationId: `${MOCK_MARKER}-op-${operationCounter}`,
    accepted: true,
    queuedAtUtc: new Date().toISOString(),
  };
}

type MockEvent = [delayMs: number, status: Partial<OperationStatus>];

function sequence(item: OptionalInstallItem, removing: boolean): MockEvent[] {
  const phase = removing ? "Removing" : "Installing";
  const queued: MockEvent = [0, { state: "Queued", message: "Waiting for the service." }];

  if (item.itemName === "DemoFailing") {
    return [
      queued,
      [400, { state: "Downloading", progressPercent: 40 }],
      [900, { state: "ItemFailed", message: "installer exited with code 1" }],
      [
        1200,
        {
          state: "Failed",
          message: "run reported a failure",
          errorCode: "install_failed",
          errorMessage: "installer exited with code 1",
        },
      ],
    ];
  }

  if (item.itemName === "DemoBlocked") {
    return [
      queued,
      [400, { state: "Downloading", progressPercent: 60 }],
      [900, { state: "Deferred", message: "blocking application is running" }],
    ];
  }

  return [
    queued,
    [300, { state: "Downloading", progressPercent: 25 }],
    // A dependency item carries its own identity and resets the percentage.
    [
      600,
      {
        state: "Installing",
        progressPercent: 10,
        itemName: "DemoUpdater",
        displayName: "Demo Updater",
      },
    ],
    [
      900,
      {
        state: "ItemCompleted",
        progressPercent: 100,
        itemName: "DemoUpdater",
        displayName: "Demo Updater",
      },
    ],
    [1200, { state: phase, progressPercent: 55 }],
    [1600, { state: "ItemCompleted", progressPercent: 100 }],
    [2000, { state: "Succeeded", message: "run completed" }],
  ];
}

function mutate(itemName: string, removing: boolean): Promise<AcceptedOperation> {
  const item = items.find((candidate) => candidate.itemName === itemName);
  if (!item) {
    return Promise.reject(new Error(`unknown item ${itemName}`));
  }
  const accepted = accept();
  const operationId = accepted.operationId ?? "";

  for (const [delay, partial] of sequence(item, removing)) {
    setTimeout(() => {
      const status: OperationStatus = {
        operationId,
        timestampUtc: new Date().toISOString(),
        itemName: item.itemName,
        displayName: item.displayName,
        state: "",
        progressPercent: 0,
        message: "",
        ...partial,
      };
      if (status.state === "Succeeded") {
        item.isInstalled = !removing;
        item.isManaged = !removing;
        item.status = removing ? "NotInstalled" : "Installed";
      }
      for (const handler of handlers) {
        handler(status);
      }
    }, delay);
  }
  return Promise.resolve(accepted);
}

export const api: GorillaApi = {
  listOptionalInstalls: () =>
    offline()
      ? Promise.reject(new Error("mock service unavailable (remove ?offline to reconnect)"))
      : new Promise((resolve) => setTimeout(() => resolve(items.map((item) => ({ ...item }))), 250)),
  installItem: (itemName) => mutate(itemName, false),
  removeItem: (itemName) => mutate(itemName, true),
  watchOperation: () => Promise.resolve(),
  onOperationStatus(handler) {
    handlers.push(handler);
  },
};
