export const CRAMPED_IFRAME_VIEWPORT = { width: 390, height: 844 } as const;

/** Typical CPA management chrome that sits over the nested plugin iframe. */
export const CPA_MANAGEMENT_CHROME_HEIGHT_PX = 88;

/** Desktop embedded clearance already used by --cpa-host-toolbar-clearance. */
export const EMBEDDED_DESKTOP_CLEARANCE_PX = 68;

export type Box = {
  x: number;
  y: number;
  width: number;
  height: number;
};

export type Viewport = {
  width: number;
  height: number;
};

export function boxesIntersect(a: Box, b: Box): boolean {
  return a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
}

export function hostChromeBox(heightPx: number, viewportWidth: number): Box {
  return { x: 0, y: 0, width: viewportWidth, height: heightPx };
}

export function pluginTabHitBox(args: {
  viewportWidth: number;
  offsetY: number;
  tabHeight?: number;
}): Box {
  return {
    x: 0,
    y: args.offsetY,
    width: args.viewportWidth,
    height: args.tabHeight ?? 38,
  };
}

export function pluginTabsAreUncovered(hostChrome: Box, tabBox: Box): boolean {
  return !boxesIntersect(hostChrome, tabBox);
}

export function embeddedTabOffsetPx(args: { embedded: boolean; viewportWidth: number }): number {
  if (!args.embedded) {
    return 0;
  }
  if (args.viewportWidth <= CRAMPED_IFRAME_VIEWPORT.width) {
    return Math.max(EMBEDDED_DESKTOP_CLEARANCE_PX, CPA_MANAGEMENT_CHROME_HEIGHT_PX);
  }
  return EMBEDDED_DESKTOP_CLEARANCE_PX;
}

export function applyEmbeddedIframeLayout(
  root: HTMLElement,
  viewport: Viewport,
  embedded: boolean,
): { clearancePx: number; tabBox: Box; uncovered: boolean } {
  const clearancePx = embeddedTabOffsetPx({ embedded, viewportWidth: viewport.width });
  root.style.setProperty("--cpa-host-toolbar-clearance", `${clearancePx}px`);
  if (embedded && viewport.width <= CRAMPED_IFRAME_VIEWPORT.width) {
    root.setAttribute("data-cramped-iframe", "");
  } else {
    root.removeAttribute("data-cramped-iframe");
  }
  const tabBox = pluginTabHitBox({ viewportWidth: viewport.width, offsetY: clearancePx });
  const hostChrome = hostChromeBox(CPA_MANAGEMENT_CHROME_HEIGHT_PX, viewport.width);
  return {
    clearancePx,
    tabBox,
    uncovered: pluginTabsAreUncovered(hostChrome, tabBox),
  };
}
