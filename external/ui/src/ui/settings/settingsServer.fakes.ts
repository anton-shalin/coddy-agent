import { createElement } from "react";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { vi } from "vitest";
import { render } from "@testing-library/react";

import { initLocale, setLocale } from "../i18n/i18n";
import {
  captureTelegramLaunch,
  resetTelegramLaunchForTests,
} from "../telegram/launch";
import { defaultForSchema, type JsonSchema } from "./SchemaForm";
import { Settings } from "./Settings";
import { resetSettingsConfigForTests } from "./settingsConfigStore";

// The schemas coddy serve hands the settings form, as the Go builders print
// them (internal/config/testdata, kept current by TestUISchemaTestdataIsCurrent).
const testdata = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../../../internal/config/testdata",
);

function readSchema(name: string): JsonSchema {
  return JSON.parse(readFileSync(join(testdata, name), "utf8")) as JsonSchema;
}

/** The schema of an agent's Settings (config.UISchemaJSON). */
export const AGENT_SCHEMA: JsonSchema = readSchema("ui_schema.json");
/** The schema of a swarm relay's Settings (config.RelayUISchemaJSON). */
export const RELAY_SCHEMA: JsonSchema = readSchema("ui_schema_relay.json");

/** Values that look like secrets; no search may ever find one. */
export const SECRET_VALUES = {
  apiKey: "sk-live-9f3c1d7a52e4b608",
  apiKeyCommand: "vault-read-llm-credential-7731",
  proxyUrl: "http://proxyuser:pr0xyp4ss@egress.corp.example:3128",
  apiBase: "https://llm-gateway.corp.example/v2",
  telegramToken: "7391204455:AAHq-telegram-bot-secret",
  braveKey: "BSA-brave-search-secret-4471",
  searxngUrl: "https://searx.corp.example/search",
  relayToken: "relay-client-token-5528",
  pairingToken: "pairing-token-3190",
} as const;

/** A document to put in front of the form: what a server would hold for a user. */
export type SettingsDoc = Record<string, unknown>;

type PlainObject = Record<string, unknown>;

function isObjectItems(s: JsonSchema): boolean {
  return (
    s.type === "array" &&
    s.items !== undefined &&
    s.items.type === "object" &&
    s.items.properties !== undefined
  );
}

/** The defaults of a schema, with one entry in every list of objects. */
function filled(s: JsonSchema): unknown {
  if (s.type === "object" && s.properties) {
    const out: PlainObject = {};
    for (const [k, sub] of Object.entries(s.properties)) {
      out[k] = filled(sub);
    }
    return out;
  }
  if (isObjectItems(s)) {
    return [filled(s.items!)];
  }
  return defaultForSchema(s);
}

function setPath(doc: PlainObject, path: string, value: unknown): void {
  const keys = path.split(".");
  let at: PlainObject = doc;
  for (const key of keys.slice(0, -1)) {
    const next = at[key];
    if (next === null || typeof next !== "object" || Array.isArray(next)) {
      return;
    }
    at = next as PlainObject;
  }
  const last = keys[keys.length - 1]!;
  if (last in at) {
    at[last] = value;
  }
}

/**
 * A configuration document for a schema: its defaults, one entry in every list
 * of objects, and secret-looking values wherever the schema has a place for
 * one. An agent's schema also gets the rows the tests need: providers of every
 * type (the first with a proxy), a row without a name and two named "shared",
 * and two logical models.
 */
export function configFromSchema(schema: JsonSchema): SettingsDoc {
  const doc = filled(schema) as SettingsDoc;
  const providers = schema.properties?.["providers"];
  if (providers?.items) {
    const row = (over: PlainObject): PlainObject => ({
      ...(filled(providers.items!) as PlainObject),
      ...over,
    });
    doc["providers"] = [
      row({
        name: "openai",
        type: "openai",
        api_base: SECRET_VALUES.apiBase,
        api_key: SECRET_VALUES.apiKey,
        api_key_command: SECRET_VALUES.apiKeyCommand,
        proxy: SECRET_VALUES.proxyUrl,
      }),
      row({ name: "anthropic", type: "anthropic" }),
      row({ name: "neuraldeep", type: "neuraldeep" }),
      row({ name: "codex", type: "codex" }),
      row({ name: "devin", type: "devin" }),
      row({ name: "", type: "openai" }),
      row({ name: "shared", type: "openai" }),
      row({ name: "shared", type: "anthropic" }),
    ];
  }
  const models = schema.properties?.["models"];
  if (models?.items) {
    const row = (model: string): PlainObject => ({
      ...(filled(models.items!) as PlainObject),
      model,
    });
    doc["models"] = [row("demo/qwen3-27b"), row("demo/llama-3")];
  }
  setPath(doc, "gateways.telegram.token", SECRET_VALUES.telegramToken);
  setPath(doc, "tools.websearch.brave_api_key", SECRET_VALUES.braveKey);
  setPath(doc, "tools.websearch.searxng_url", SECRET_VALUES.searxngUrl);
  setPath(doc, "swarm.auth_token", SECRET_VALUES.relayToken);
  setPath(doc, "swarm.pairing_tokens", [SECRET_VALUES.pairingToken]);
  return doc;
}

export type ServerState = {
  schema: unknown;
  config: PlainObject;
  /** What PUT /coddy/config answers; ok unless a test says otherwise. */
  put: { status: number; body: PlainObject };
};

export function newServerState(
  init: { schema: unknown; config: PlainObject } & Partial<ServerState>,
): ServerState {
  return { put: { status: 200, body: { ok: true } }, ...init };
}

/**
 * A coddy serve behind fetch: the schema, the config document, a save. The
 * state is read on every request, so a test that changes it (or hands a getter
 * that returns its current state) is heard by the next read.
 */
export function stubServer(source: ServerState | (() => ServerState)) {
  const state = typeof source === "function" ? source : () => source;
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    const server = state();
    const method = init?.method ?? "GET";
    const reply = (status: number, body: unknown) => ({
      ok: status >= 200 && status < 300,
      status,
      json: async () => structuredClone(body),
    });
    if (path === "/coddy/config/schema") return reply(200, server.schema);
    if (path === "/coddy/config" && method === "GET") {
      return reply(200, server.config);
    }
    if (path === "/coddy/config/validate") return reply(200, { ok: true });
    if (path === "/coddy/config" && method === "PUT") {
      if (server.put.status === 200) {
        server.config = JSON.parse(String(init?.body)) as PlainObject;
      }
      return reply(server.put.status, server.put.body);
    }
    return reply(200, { ok: true, models: [] });
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

// ---- the page around the drawer: viewport, input device, language, Telegram

type Viewport = { width: number; touch: boolean };

function conditionHolds(condition: string, vp: Viewport): boolean {
  const px = /^\(\s*(max|min)-width:\s*(\d+)px\s*\)$/.exec(condition);
  if (px) {
    const limit = Number(px[2]);
    return px[1] === "max" ? vp.width <= limit : vp.width >= limit;
  }
  switch (condition.replace(/\s+/g, " ")) {
    case "(any-hover: none)":
    case "(hover: none)":
    case "(any-pointer: coarse)":
    case "(pointer: coarse)":
      return vp.touch;
    case "(any-hover: hover)":
    case "(hover: hover)":
    case "(any-pointer: fine)":
    case "(pointer: fine)":
      return !vp.touch;
    default:
      return false;
  }
}

/** Whether a media query holds on a viewport; the ones the SPA asks, nothing more. */
function queryHolds(query: string, vp: Viewport): boolean {
  return query.split(",").some((group) =>
    group
      .trim()
      .split(/\s+and\s+/)
      .every((condition) => conditionHolds(condition.trim(), vp)),
  );
}

const originalMatchMedia = window.matchMedia;

/** Makes window.matchMedia answer for a viewport width and an input device. */
export function setViewport(vp: Partial<Viewport> = {}): void {
  const viewport: Viewport = { width: 1280, touch: false, ...vp };
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: queryHolds(query, viewport),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
}

const TELEGRAM_FRAGMENT = new URLSearchParams({
  tgWebAppData: "query_id=q1&user=%7B%22id%22%3A4242%7D&auth_date=1&hash=abc",
  tgWebAppVersion: "10.1",
  tgWebAppPlatform: "ios",
}).toString();

/** Opens the page as a Telegram Mini App: a captured launch, as telegram.test.tsx does. */
export function launchAsTelegram(): void {
  window.history.replaceState(null, "", `/#${TELEGRAM_FRAGMENT}`);
  captureTelegramLaunch(window);
}

export type PageOptions = {
  /** Viewport width in px; 1280 is the rail, up to 1199 the stacked shell. */
  width?: number;
  /** A touch-only device: no input can hover and a pointer is coarse. */
  touch?: boolean;
  lang?: "en" | "ru";
  telegram?: boolean;
};

/** Puts the page around a drawer in the state the options name. */
export function applyPage(opts: PageOptions = {}): void {
  initLocale("en");
  setLocale(opts.lang ?? "en");
  setViewport({
    ...(opts.width !== undefined ? { width: opts.width } : {}),
    ...(opts.touch !== undefined ? { touch: opts.touch } : {}),
  });
  resetTelegramLaunchForTests();
  window.sessionStorage.clear();
  if (opts.telegram) {
    launchAsTelegram();
  }
}

/** Undoes applyPage; call it from afterEach. */
export function resetPage(): void {
  window.matchMedia = originalMatchMedia;
  resetTelegramLaunchForTests();
  window.sessionStorage.clear();
  window.history.replaceState(null, "", "/");
  setLocale("en");
}

export type MountOptions = PageOptions & {
  schema?: JsonSchema;
  config?: SettingsDoc;
  /** The tab and the row the address names (`#/settings/<section>?id=<item>`). */
  section?: string | null;
  item?: string | null;
  onClose?: () => void;
};

/**
 * Mounts the Settings drawer over a stubbed server: the schema and the config
 * document (by default the agent's schema and configFromSchema of it), on a
 * page of the viewport, language and launch the options name. A relay's schema
 * mounts the relay's drawer.
 */
export function mountSettings(opts: MountOptions = {}) {
  const schema = opts.schema ?? AGENT_SCHEMA;
  const config = opts.config ?? configFromSchema(schema);
  applyPage(opts);
  resetSettingsConfigForTests();
  const server = newServerState({ schema, config });
  const fetch = stubServer(server);
  const view = render(
    createElement(Settings, {
      onClose: opts.onClose ?? (() => {}),
      initialSection: opts.section ?? null,
      initialItem: opts.item ?? null,
      relay: schema["x-coddy-relay"] === true,
    }),
  );
  return { ...view, fetch, server };
}
