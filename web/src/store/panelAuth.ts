const ENC_PREFIX_V1 = "enc::v1::";
const ENC_PREFIX_V2 = "enc::v2::";
const SECRET_SALT = "cli-proxy-api-webui::secure-storage";
const STORAGE_KEY = "cli-proxy-auth";

export interface PanelAuth {
  apiBase: string;
  managementKey: string;
}

function isEmbedded(): boolean {
  try {
    return window.self !== window.top;
  } catch {
    return false;
  }
}

function xorBytes(data: Uint8Array, key: Uint8Array): Uint8Array {
  const output = new Uint8Array(data.length);
  for (let index = 0; index < data.length; index += 1) {
    output[index] = data[index] ^ key[index % key.length];
  }
  return output;
}

// The host Management Center stores its session with the same XOR scheme in two
// flavors: v2 keys on salt|host, while the legacy v1 also folded in the user
// agent. New entries are written as v2 so a browser UA change cannot orphan them,
// and both flavors stay readable like the host does.
function storageKeyMaterial(includeUserAgent: boolean): Uint8Array {
  const base = `${SECRET_SALT}|${window.location.host}`;
  return new TextEncoder().encode(includeUserAgent ? `${base}|${navigator.userAgent}` : base);
}

function decodeStoredValue(value: string): string {
  let prefix = ENC_PREFIX_V2;
  let includeUserAgent = false;
  if (value.startsWith(ENC_PREFIX_V1)) {
    prefix = ENC_PREFIX_V1;
    includeUserAgent = true;
  } else if (!value.startsWith(ENC_PREFIX_V2)) {
    return value;
  }
  const binary = atob(value.slice(prefix.length));
  const encrypted = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  return new TextDecoder().decode(xorBytes(encrypted, storageKeyMaterial(includeUserAgent)));
}

function encodeStoredValue(value: string): string {
  const encrypted = xorBytes(new TextEncoder().encode(value), storageKeyMaterial(false));
  const binary = Array.from(encrypted, (character) => String.fromCharCode(character)).join("");
  return `${ENC_PREFIX_V2}${btoa(binary)}`;
}

function extractPanelAuth(value: unknown): PanelAuth | null {
  if (!value || typeof value !== "object") return null;
  const root = value as Record<string, unknown>;
  const state = (root.state as Record<string, unknown> | undefined) ?? root;
  const apiBase = typeof state.apiBase === "string" ? state.apiBase.trim() : "";
  const managementKey = typeof state.managementKey === "string" ? state.managementKey.trim() : "";
  if (apiBase === "" || managementKey === "") return null;
  return { apiBase, managementKey };
}

export function readPanelAuth(options: { allowStandalone?: boolean } = {}): PanelAuth | null {
  if (!isEmbedded() && options.allowStandalone !== true) return null;
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    return extractPanelAuth(JSON.parse(decodeStoredValue(raw)));
  } catch {
    return null;
  }
}

export function writePanelAuth(apiBase: string, managementKey: string): void {
  const auth = {
    apiBase: apiBase.trim().replace(/\/+$/, ""),
    managementKey: managementKey.trim(),
  };
  if (auth.apiBase === "" || auth.managementKey === "") return;
  try {
    const state = { ...auth, rememberPassword: true };
    localStorage.setItem(STORAGE_KEY, encodeStoredValue(JSON.stringify({ state, version: 0 })));
    // The host Management Center uses this marker to re-enter a remembered session.
    localStorage.setItem("isLoggedIn", "true");
  } catch {
    // Browser storage is unavailable; the in-memory session still works.
  }
}

export function clearPanelAuth(): void {
  try {
    localStorage.removeItem(STORAGE_KEY);
    localStorage.removeItem("isLoggedIn");
  } catch {
    // Browser storage is unavailable; the in-memory session is already cleared.
  }
}
