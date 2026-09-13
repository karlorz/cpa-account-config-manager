import { applyEmbeddedIframeLayout } from "../layout/embeddedIframeLayout";

type Theme = "light" | "white" | "dark";

function embedded(): boolean {
  try {
    return window.self !== window.top;
  } catch {
    return false;
  }
}

function parentTheme(): Theme {
  if (!embedded()) return "light";
  try {
    const value = window.parent.document.documentElement.getAttribute("data-theme");
    return value === "dark" || value === "white" ? value : "light";
  } catch {
    return "light";
  }
}

function applyTheme(theme: Theme): void {
  if (theme === "light") document.documentElement.removeAttribute("data-theme");
  else document.documentElement.setAttribute("data-theme", theme);
}

function syncEmbeddedIframeLayout(isEmbedded: boolean): void {
  applyEmbeddedIframeLayout(
    document.documentElement,
    { width: window.innerWidth, height: window.innerHeight },
    isEmbedded,
  );
}

export function initThemeSync(): () => void {
  const isEmbedded = embedded();
  document.documentElement.setAttribute("data-plugin-host", isEmbedded ? "cpa" : "standalone");
  applyTheme(isEmbedded ? parentTheme() : "light");
  syncEmbeddedIframeLayout(isEmbedded);
  const onResize = () => syncEmbeddedIframeLayout(isEmbedded);
  window.addEventListener("resize", onResize);
  if (!isEmbedded) {
    return () => window.removeEventListener("resize", onResize);
  }
  try {
    const element = window.parent.document.documentElement;
    const observer = new MutationObserver(() => applyTheme(parentTheme()));
    observer.observe(element, { attributes: true, attributeFilter: ["data-theme"] });
    return () => {
      window.removeEventListener("resize", onResize);
      observer.disconnect();
    };
  } catch {
    return () => window.removeEventListener("resize", onResize);
  }
}
