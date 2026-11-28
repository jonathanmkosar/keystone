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
// The admin account's pages are drawn in place of the person's app (P15-01).
import { AdminApp, isAdminPath } from "./AdminApp";
// A new person's setup page, with the code an admin gave them (P15-02).
import { isSetupPath, SetupPage } from "./SetupPage";

const path = window.location.pathname;
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      {isSharedPath(path) ? <SharedPage /> : isAdminPath(path) ? <AdminApp /> : isSetupPath(path) ? <SetupPage /> : <App />}
    </BrowserRouter>
  </StrictMode>,
);
