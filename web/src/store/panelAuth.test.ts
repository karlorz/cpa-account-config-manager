import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { clearPanelAuth, readPanelAuth, writePanelAuth } from "./panelAuth";

const storageKey = "cli-proxy-auth";
const prefixV1 = "enc::v1::";
const prefixV2 = "enc::v2::";
const salt = "cli-proxy-api-webui::secure-storage";

function xorEncode(raw: string, keyMaterial: string, prefix: string): string {
  const data = new TextEncoder().encode(raw);
  const key = new TextEncoder().encode(keyMaterial);
  const encrypted = new Uint8Array(data.length);
  for (let index = 0; index < data.length; index += 1) {
    encrypted[index] = data[index] ^ key[index % key.length];
  }
  const binary = Array.from(encrypted, (value) => String.fromCharCode(value)).join("");
  return `${prefix}${btoa(binary)}`;
}

// The legacy host flavor: salt|host|userAgent under enc::v1::.
function encodePanelState(apiBase: string, managementKey: string): string {
  const raw = JSON.stringify({ state: { apiBase, managementKey, rememberPassword: true }, version: 0 });
  return xorEncode(raw, `${salt}|${window.location.host}|${navigator.userAgent}`, prefixV1);
}

// The current host flavor: salt|host under enc::v2::, mirroring the Management
// Center's own writer so this test catches a drift between the two codebases.
function encodePanelStateV2(apiBase: string, managementKey: string): string {
  const raw = JSON.stringify({ state: { apiBase, managementKey, rememberPassword: true, serverVersion: "v0" }, version: 0 });
  return xorEncode(raw, `${salt}|${window.location.host}`, prefixV2);
}

function decodeWithHostAlgorithm(value: string): unknown {
  const isV1 = value.startsWith(prefixV1);
  const keyMaterial = isV1 ? `${salt}|${window.location.host}|${navigator.userAgent}` : `${salt}|${window.location.host}`;
  const binary = atob(value.slice(9));
  const encrypted = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  const key = new TextEncoder().encode(keyMaterial);
  const output = new Uint8Array(encrypted.length);
  for (let index = 0; index < encrypted.length; index += 1) {
    output[index] = encrypted[index] ^ key[index % key.length];
  }
  return JSON.parse(new TextDecoder().decode(output));
}

describe("official panel auth recovery", () => {
  const realSelf = window.self;
  const realTop = window.top;

  beforeEach(() => localStorage.clear());

  afterEach(() => {
    localStorage.clear();
    Object.defineProperty(window, "self", { value: realSelf, configurable: true });
    Object.defineProperty(window, "top", { value: realTop, configurable: true });
  });

  function setEmbedded(embedded: boolean) {
    Object.defineProperty(window, "self", { value: window, configurable: true });
    Object.defineProperty(window, "top", { value: embedded ? ({} as Window) : window, configurable: true });
  }

  it("does not read remembered credentials when opened directly", () => {
    setEmbedded(false);
    localStorage.setItem(storageKey, encodePanelState("http://127.0.0.1:8317", "test-key"));
    expect(readPanelAuth()).toBeNull();
  });

  it("reads same-origin credentials in an explicitly authorized standalone OAuth window", () => {
    setEmbedded(false);
    localStorage.setItem(storageKey, encodePanelState("http://127.0.0.1:8317", "test-key"));
    expect(readPanelAuth({ allowStandalone: true })).toEqual({ apiBase: "http://127.0.0.1:8317", managementKey: "test-key" });
    expect(readPanelAuth()).toBeNull();
  });

  it("recovers the official panel session only in a same-origin iframe", () => {
    setEmbedded(true);
    localStorage.setItem(storageKey, encodePanelState("http://127.0.0.1:8317", "test-key"));
    expect(readPanelAuth()).toEqual({ apiBase: "http://127.0.0.1:8317", managementKey: "test-key" });
  });

  it("fails closed for malformed or incomplete remembered state", () => {
    setEmbedded(true);
    localStorage.setItem(storageKey, "not-json");
    expect(readPanelAuth()).toBeNull();
    localStorage.setItem(storageKey, encodePanelState("http://127.0.0.1:8317", ""));
    expect(readPanelAuth()).toBeNull();
  });

  it("persists a verified manual login in the host-compatible encrypted session", () => {
    writePanelAuth(" https://cpa.example/ ", " test-key ");

    expect(readPanelAuth({ allowStandalone: true })).toEqual({ apiBase: "https://cpa.example", managementKey: "test-key" });
    expect(localStorage.getItem("isLoggedIn")).toBe("true");
  });

  it("reads the current host v2 session flavor", () => {
    setEmbedded(true);
    localStorage.setItem(storageKey, encodePanelStateV2("https://clifree.example", "test-key"));

    expect(readPanelAuth()).toEqual({ apiBase: "https://clifree.example", managementKey: "test-key" });
  });

  it("writes the v2 flavor the Management Center reads back", () => {
    writePanelAuth("https://clifree.example", "test-key");

    const raw = localStorage.getItem(storageKey);
    expect(raw?.startsWith(prefixV2)).toBe(true);
    expect(decodeWithHostAlgorithm(String(raw))).toEqual({
      state: { apiBase: "https://clifree.example", managementKey: "test-key", rememberPassword: true },
      version: 0,
    });
  });

  it("clears the remembered session after a rejected key", () => {
    writePanelAuth("https://cpa.example", "test-key");

    clearPanelAuth();

    expect(readPanelAuth({ allowStandalone: true })).toBeNull();
    expect(localStorage.getItem("isLoggedIn")).toBeNull();
  });
});
