import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { ScreenPage } from "./screen-page";
import { screenTaskOf } from "./screen-address";
import "./styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("CEO board root is missing");
// An address that names a goblin's screen shows that screen alone, full size.
const screenTask = screenTaskOf(location.search);
createRoot(root).render(
  <StrictMode>
    {screenTask ? <ScreenPage task={screenTask} /> : <App />}
  </StrictMode>,
);
