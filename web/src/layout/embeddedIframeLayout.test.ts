import { afterEach, describe, expect, it } from "vitest";
import {
  CPA_MANAGEMENT_CHROME_HEIGHT_PX,
  CRAMPED_IFRAME_VIEWPORT,
  applyEmbeddedIframeLayout,
  hostChromeBox,
  pluginTabHitBox,
  pluginTabsAreUncovered,
} from "./embeddedIframeLayout";

afterEach(() => {
  document.documentElement.removeAttribute("data-cramped-iframe");
  document.documentElement.style.removeProperty("--cpa-host-toolbar-clearance");
});

describe("embedded iframe tab overlay at 390×844", () => {
  it("keeps nested plugin tab hit-targets uncovered by host management chrome", () => {
    const viewport = { ...CRAMPED_IFRAME_VIEWPORT };
    const hostChrome = hostChromeBox(CPA_MANAGEMENT_CHROME_HEIGHT_PX, viewport.width);

    const layout = applyEmbeddedIframeLayout(document.documentElement, viewport, true);

    expect(layout.tabBox).toEqual(
      pluginTabHitBox({ viewportWidth: viewport.width, offsetY: layout.clearancePx }),
    );
    expect(pluginTabsAreUncovered(hostChrome, layout.tabBox)).toBe(true);
    expect(layout.uncovered).toBe(true);
    expect(document.documentElement).toHaveAttribute("data-cramped-iframe", "");
    expect(document.documentElement.style.getPropertyValue("--cpa-host-toolbar-clearance")).toBe(
      `${layout.clearancePx}px`,
    );
    expect(layout.clearancePx).toBeGreaterThanOrEqual(CPA_MANAGEMENT_CHROME_HEIGHT_PX);
  });

  it("does not reserve host chrome on standalone pages", () => {
    const layout = applyEmbeddedIframeLayout(document.documentElement, { ...CRAMPED_IFRAME_VIEWPORT }, false);
    expect(layout.clearancePx).toBe(0);
    expect(document.documentElement.hasAttribute("data-cramped-iframe")).toBe(false);
  });
});
