import { UIService } from "../bindings/github.com/1dustindavis/gorilla/gorilla-ui/index.js";

export function listOptionalInstalls() {
  return UIService.ListOptionalInstalls();
}
