import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import "virtual:tokens.css";
import "./fonts.css";
import "./styles.css";
import "./chat.css";
import "./settings.css";
import { App } from "./App";
// A share link's page is drawn in place of the app, before anything asks who is signed in (P10-04).
import { isSharedPath, SharedPage } from "./Shared";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      {isSharedPath(window.location.pathname) ? <SharedPage /> : <App />}
    </BrowserRouter>
  </StrictMode>,
);
