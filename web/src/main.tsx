import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./app/App.tsx";

const root = document.getElementById("root");
if (!root) {
  throw new Error("#root がない");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
