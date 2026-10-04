// GORILLA_VITE_MOCK_ONLY — this module exists so `npm run dev` can render the
// UI in a plain browser. vite.config.ts resolves it only in development mode,
// and the production-bundle gate greps for this marker.
import type {
  AcceptedOperation,
  GorillaApi,
  OperationStatus,
  OptionalInstallItem,
} from "./api.ts";
import { isTerminalState } from "./state.ts";

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
    isRequired: false,
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
    isRequired: false,
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
    isRequired: false,
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
    isRequired: false,
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
    isRequired: false,
    isInstalled: false,
    status: "WillBeInstalled",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
  {
    itemName: "DemoPendingRemoval",
    displayName: "Demo Pending Removal",
    version: "1.0.1",
    catalog: "selfserve_catalog",
    description: "Already requested for removal; its primary action is Cancel.",
    category: "Utilities",
    developer: "Gorilla",
    isManaged: true,
    isRequired: false,
    isInstalled: true,
    status: "WillBeRemoved",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
  {
    itemName: "DemoNoIcon",
    displayName: "7",
    version: "7.0.0",
    catalog: "selfserve_catalog",
    description:
      "Exercises the monogram fallback and a stream that ends before any terminal record.",
    isManaged: false,
    isRequired: false,
    isInstalled: false,
    status: "Unknown",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
  {
    itemName: "DemoRequired",
    displayName: "Demo Required",
    version: "5.0.0",
    catalog: "selfserve_catalog",
    description: "Required by the organisation: no Install or Remove, only Details.",
    category: "Security",
    developer: "Gorilla",
    isManaged: false,
    isRequired: true,
    isInstalled: true,
    status: "Installed",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
  },
];

const handlers: ((status: OperationStatus) => void)[] = [];
let operationCounter = 0;

function branded(): boolean {
  return globalThis.location?.search.includes("branded") ?? false;
}

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
      [900, { state: "Deferred", message: "blocking application(s) running: demoblocked" }],
    ];
  }

  // No terminal record: the watch call rejects, exercising the stream_ended UI.
  if (item.itemName === "DemoNoIcon") {
    return [queued, [400, { state: "Downloading", progressPercent: 30 }]];
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

const acceptedOperations = new Map<string, { item: OptionalInstallItem; removing: boolean }>();

function mutate(itemName: string, removing: boolean): Promise<AcceptedOperation> {
  const item = items.find((candidate) => candidate.itemName === itemName);
  if (!item) {
    return Promise.reject(new Error(`unknown item ${itemName}`));
  }
  const accepted = accept();
  acceptedOperations.set(accepted.operationId ?? "", { item, removing });
  return Promise.resolve(accepted);
}

// Watched operations, for cancelOperation: pending timers, the state last sent,
// and how to settle the watch call.
type Watched = {
  item: OptionalInstallItem;
  state: string;
  // seq is the last record number sent, as the service numbers them.
  seq: number;
  timers: ReturnType<typeof setTimeout>[];
  settle: () => void;
};
const watched = new Map<string, Watched>();

function emit(status: OperationStatus): void {
  for (const handler of handlers) {
    handler(status);
  }
}

/**
 * watch replays a sequence through the same callback production uses, so the
 * mock exercises the real routing: nothing is delivered until the UI watches,
 * and a sequence with no terminal record rejects like a premature stream end.
 */
function watch(operationId: string): Promise<void> {
  const request = acceptedOperations.get(operationId);
  if (!request) {
    return Promise.reject(new Error(`unknown operation ${operationId}`));
  }
  acceptedOperations.delete(operationId);
  const { item, removing } = request;
  const events = sequence(item, removing);
  const [lastDelay, lastPartial] = events[events.length - 1];

  return new Promise((resolve, reject) => {
    const entry: Watched = { item, state: "", seq: 0, timers: [], settle: resolve };
    watched.set(operationId, entry);
    for (const [delay, partial] of events) {
      entry.timers.push(
        setTimeout(() => {
          const status: OperationStatus = {
            operationId,
            seq: ++entry.seq,
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
          entry.state = status.state;
          emit(status);
        }, delay),
      );
    }
    entry.timers.push(
      setTimeout(() => {
        watched.delete(operationId);
        if (isTerminalState(lastPartial.state ?? "")) {
          resolve();
        } else {
          reject(new Error("operation stream ended before a terminal event"));
        }
      }, lastDelay + 1),
    );
  });
}

/** cancel mirrors the service: accepted until the installer starts, refused after. */
function cancel(operationId: string): Promise<void> {
  const entry = watched.get(operationId);
  if (!entry) {
    return Promise.reject(new Error("operation_not_cancelable: operation can no longer be canceled: it has already finished"));
  }
  if (!["", "Queued", "Downloading"].includes(entry.state)) {
    return Promise.reject(
      new Error(
        `operation_not_cancelable: operation can no longer be canceled: work on ${entry.item.displayName} has already started`,
      ),
    );
  }
  entry.timers.forEach(clearTimeout);
  watched.delete(operationId);
  emit({
    operationId,
    seq: entry.seq + 1,
    timestampUtc: new Date().toISOString(),
    itemName: entry.item.itemName,
    displayName: entry.item.displayName,
    state: "Canceled",
    progressPercent: 0,
    message: "Canceled by user",
    canceledBy: "user",
  });
  entry.settle();
  return Promise.resolve();
}

export const api: GorillaApi = {
  listOptionalInstalls: () =>
    offline()
      ? Promise.reject(new Error("mock service unavailable (remove ?offline to reconnect)"))
      : new Promise((resolve) => setTimeout(() => resolve(items.map((item) => ({ ...item }))), 250)),
  installItem: (itemName) => mutate(itemName, false),
  removeItem: (itemName) => mutate(itemName, true),
  watchOperation: (operationId) => watch(operationId),
  cancelOperation: (operationId) => cancel(operationId),
  // `?branded` shows the Branding board's organisation banner.
  getBranding: () =>
    Promise.resolve({
      title: branded() ? "Acme Software Center" : "",
      tagline: branded() ? "Need help? Call the service desk at ext. 1234." : "",
      helpUrl: branded() ? "https://example.invalid/help" : "",
      helpLabel: branded() ? "Get help" : "",
      accent: branded() ? "#0b6e4f" : "",
      logoMime: "",
      logoBase64: "",
    }),
  openExternal(url) {
    window.open(url, "_blank", "noopener,noreferrer");
    return Promise.resolve();
  },
  onOperationStatus(handler) {
    handlers.push(handler);
  },
};
