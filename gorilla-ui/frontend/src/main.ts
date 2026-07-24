import { listOptionalInstalls } from "./wails-api";

const status = document.querySelector<HTMLParagraphElement>("#service-status");
const itemList = document.querySelector<HTMLUListElement>("#optional-items");

if (!status || !itemList) {
  throw new Error("Gorilla UI shell is incomplete");
}

try {
  const items = await listOptionalInstalls();
  status.textContent = "Connected to the Gorilla service.";
  for (const item of items) {
    const row = document.createElement("li");
    row.textContent = item.displayName;
    itemList.append(row);
  }
} catch (error) {
  status.textContent = `Service unavailable: ${error instanceof Error ? error.message : String(error)}`;
}
