import { useEffect, useState } from "react";

export type ThemeChoice = "dark" | "light" | "system";

// The same key public/theme.js reads before the page paints.
const key = "keystone.theme";

function stored(): ThemeChoice {
  try {
    const t = localStorage.getItem(key);
    if (t === "dark" || t === "light") return t;
  } catch {
    // Storage can be blocked. The theme then follows the system.
  }
  return "system";
}

function apply(choice: ThemeChoice): void {
  const root = document.documentElement;
  if (choice === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", choice);
  try {
    if (choice === "system") localStorage.removeItem(key);
    else localStorage.setItem(key, choice);
  } catch {
    // The choice still holds for this page.
  }
}

/** The theme choice, kept in the browser, and a function to change it. */
export function useTheme(): [ThemeChoice, (choice: ThemeChoice) => void] {
  const [choice, setChoice] = useState<ThemeChoice>(stored);
  useEffect(() => apply(choice), [choice]);
  return [choice, setChoice];
}
