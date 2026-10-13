import { afterEach, beforeEach, expect, test } from "vitest";

import { setLocale, translate } from "../i18n/i18n";
import { messagesEn } from "../i18n/messages/en";
import { messagesRu } from "../i18n/messages/ru";
import { resetTelegramLaunchForTests } from "../telegram/launch";
import {
  buildSettingsSearchIndex,
  descriptionLine,
  makeSearchEntry,
  markWords,
  normalizeSearchText,
  searchSettings,
  searchWords,
  settingsIndexRowsKey,
  SETTINGS_LANDMARKS,
  SETTINGS_SEARCH_LIMIT,
  type SettingsSearchEntry,
} from "./settingsSearch";
import {
  AGENT_SCHEMA,
  RELAY_SCHEMA,
  SECRET_VALUES,
  configFromSchema,
  launchAsTelegram,
  type SettingsDoc,
} from "./settingsServer.fakes";
import { deriveSettingsSections } from "./settingsSections";
import { SETTINGS_TEXTS, landmarkAnchor } from "./settingsTexts";
import { schemaFieldLabel } from "./schemaI18n";
import {
  entriesInSchemaOrder,
  fieldAnchor,
  groupAnchor,
  type JsonSchema,
} from "./SchemaForm";
import { objectSectionGroups } from "./SettingsSection";

beforeEach(() => {
  setLocale("en");
  resetTelegramLaunchForTests();
});

afterEach(() => {
  setLocale("en");
  resetTelegramLaunchForTests();
});

let serial = 0;

/** One entry of a hand-made index, built the way the index builds its own. */
function entry(
  over: Partial<Parameters<typeof makeSearchEntry>[0]> & { names: string[] },
): SettingsSearchEntry {
  serial += 1;
  return makeSearchEntry({
    id: `entry-${serial}`,
    kind: "field",
    section: "tools",
    ...over,
  });
}

/** What a search found, as the primary names in the order it shows them. */
function found(
  index: readonly SettingsSearchEntry[],
  query: string,
  limit?: number,
): string[] {
  return searchSettings(index, query, limit).hits.map((h) => h.entry.names[0]!);
}

function indexOf(
  schema: JsonSchema,
  doc: SettingsDoc = configFromSchema(schema),
): SettingsSearchEntry[] {
  return buildSettingsSearchIndex({
    sections: deriveSettingsSections(schema),
    schema,
    doc,
  });
}

function entriesAt(
  index: readonly SettingsSearchEntry[],
  anchor: string,
): SettingsSearchEntry[] {
  return index.filter((e) => e.anchor === anchor);
}

function only(
  index: readonly SettingsSearchEntry[],
  anchor: string,
  row: string | null = null,
): SettingsSearchEntry {
  const matches = entriesAt(index, anchor).filter((e) => e.row === row);
  expect(matches.map((e) => e.id)).toHaveLength(1);
  return matches[0]!;
}

// ---- matching, on hand-made entries

test("every word of the query has to occur in the own names, the config key, the surrounding names or the descriptions", () => {
  const proxy = entry({
    names: ["Proxy URL"],
    keyPath: "providers[].proxy",
    where: ["LLM providers", "openai", "Advanced settings"],
    descriptions: ["Route requests through this proxy server"],
  });
  const index = [proxy];

  expect(found(index, "proxy")).toEqual(["Proxy URL"]);
  // the config key
  expect(found(index, "providers[]")).toEqual(["Proxy URL"]);
  // the names around it
  expect(found(index, "openai")).toEqual(["Proxy URL"]);
  expect(found(index, "advanced openai")).toEqual(["Proxy URL"]);
  // the descriptions
  expect(found(index, "route")).toEqual(["Proxy URL"]);
  // a word is a substring, and words may come from different places
  expect(found(index, "prox")).toEqual(["Proxy URL"]);
  expect(found(index, "proxy openai route")).toEqual(["Proxy URL"]);
  // every word has to occur somewhere
  expect(found(index, "proxy nowhere")).toEqual([]);
  expect(found(index, "nowhere")).toEqual([]);
  // the word order of the query is free
  expect(found(index, "route proxy")).toEqual(["Proxy URL"]);
  // words do not join across the borders between two names
  expect(found([entry({ names: ["Max", "turns"] })], "maxturns")).toEqual([]);
});

test("own names and config keys come first with tabs and blocks ahead, then matches needing the surrounding names, then the descriptions", () => {
  const index = [
    entry({ names: ["Dir"], where: ["Memory"] }),
    entry({
      names: ["Wait"],
      descriptions: ["How long a memory run is waited for"],
    }),
    entry({ names: ["Memory model"] }),
    entry({ kind: "section", names: ["Memory"], anchor: null }),
    entry({ kind: "group", names: ["Memory runs"], anchor: "group:m:runs" }),
    entry({ names: ["Directory"], keyPath: "memory.dir" }),
    entry({ kind: "landmark", names: ["Memory tools"] }),
    entry({ kind: "row", names: ["memory"], row: "memory" }),
    entry({ names: ["Needs both"], where: ["Memory"], descriptions: ["x"] }),
  ];

  // tier 1: tabs, blocks and landmarks in the drawer's order, then the rest in
  // the drawer's order; tier 2: the surrounding names; tier 3: descriptions
  expect(found(index, "memory")).toEqual([
    "Memory",
    "Memory runs",
    "Memory tools",
    "Memory model",
    "Directory",
    "memory",
    "Dir",
    "Needs both",
    "Wait",
  ]);
  const tiers = searchSettings(index, "memory").hits.map((h) => h.tier);
  expect(tiers).toEqual([1, 1, 1, 1, 1, 1, 2, 2, 3]);

  // a word needing the descriptions makes the whole match tier 3
  const both = searchSettings(index, "memory x").hits;
  expect(both.map((h) => [h.entry.names[0], h.tier])).toEqual([
    ["Needs both", 3],
  ]);
});

test("case and ё do not matter, and nothing fuzzy matches", () => {
  expect(normalizeSearchText("ЁЖ Ёж Её")).toBe("еж еж ее");
  const index = [
    entry({ names: ["Ёлка и Ежик"] }),
    entry({ names: ["Max Turns"] }),
    entry({ names: ["Café"] }),
  ];

  expect(found(index, "ёЛКА")).toEqual(["Ёлка и Ежик"]);
  expect(found(index, "елка")).toEqual(["Ёлка и Ежик"]);
  expect(found(index, "ЕЖИК")).toEqual(["Ёлка и Ежик"]);
  expect(found(index, "MAX turns")).toEqual(["Max Turns"]);
  // no folding of anything else: not accents, not transposed or missing letters
  expect(found(index, "cafe")).toEqual([]);
  expect(found(index, "mxa")).toEqual([]);
  expect(found(index, "trns")).toEqual([]);
  expect(found(index, "ёлки")).toEqual([]);
  // the Latin e is not the Cyrillic е
  expect(found([entry({ names: ["Елка"] })], "elka")).toEqual([]);
});

test("a query of spaces searches nothing", () => {
  const index = [entry({ names: ["Max turns"] })];

  for (const query of ["", " ", "   ", "\t\n ", " "]) {
    expect(searchSettings(index, query)).toEqual({ hits: [], more: 0 });
  }
  expect(searchWords("  Max   TURNS ")).toEqual(["max", "turns"]);
  expect(searchWords("   ")).toEqual([]);
  expect(found(index, "  turns  ")).toEqual(["Max turns"]);
});

test("past the limit the matches are counted, not shown", () => {
  const index = Array.from({ length: 120 }, (_, i) =>
    entry({ names: [`Field ${i}`] }),
  );

  expect(SETTINGS_SEARCH_LIMIT).toBe(50);
  const shown = searchSettings(index, "field");
  expect(shown.hits).toHaveLength(50);
  expect(shown.more).toBe(70);
  // the ones shown are the first of the drawer's order
  expect(shown.hits[0]!.entry.names[0]).toBe("Field 0");
  expect(shown.hits[49]!.entry.names[0]).toBe("Field 49");

  expect(searchSettings(index, "field", 10)).toMatchObject({ more: 110 });
  expect(searchSettings(index, "field 119", 10).more).toBe(0);
  expect(searchSettings(index, "field", 200)).toMatchObject({ more: 0 });
  expect(searchSettings(index, "field", 200).hits).toHaveLength(120);

  // the limit cuts the sorted list: a better tier is never pushed out
  const late = [
    ...Array.from({ length: 60 }, (_, i) =>
      entry({ names: [`Plain ${i}`], descriptions: ["target"] }),
    ),
    entry({ names: ["Target"] }),
  ];
  expect(found(late, "target")[0]).toBe("Target");
});

test("the marked parts of a text are exactly the words of the query", () => {
  const parts = (text: string, words: string[]) =>
    markWords(text, words).map((p) => [p.text, p.hit]);

  expect(parts("Ignore system proxy", ["proxy", "ign"])).toEqual([
    ["Ign", true],
    ["ore system ", false],
    ["proxy", true],
  ]);
  // the text keeps its own case, the match does not depend on it
  expect(parts("PROXY url", ["proxy"])).toEqual([
    ["PROXY", true],
    [" url", false],
  ]);
  // overlapping and repeated words make one mark
  expect(parts("proxy proxy", ["pro", "proxy", "prox"])).toEqual([
    ["proxy", true],
    [" ", false],
    ["proxy", true],
  ]);
  expect(parts("Ёлка", ["елка"])).toEqual([["Ёлка", true]]);
  expect(parts("nothing to mark", ["zzz"])).toEqual([
    ["nothing to mark", false],
  ]);
  expect(parts("nothing", [])).toEqual([["nothing", false]]);
  expect(parts("", ["a"])).toEqual([]);
  // whatever the words, the parts are the text
  for (const text of ["Max turns (i)", "İstanbul İ", "a.b.c", "ёЁёе"]) {
    expect(
      markWords(text, searchWords("a b i е"))
        .map((p) => p.text)
        .join(""),
    ).toBe(text);
  }
});

test("a description-only match shows its description from shortly before the word", () => {
  const text =
    "Sends every request of this row through a corporate egress proxy, so nothing leaves the network directly, whatever the rest of the settings say.";
  const line = descriptionLine(text, ["egress"]);

  expect(line.startsWith("…")).toBe(true);
  const rest = line.slice(1);
  expect(text.endsWith(rest)).toBe(true);
  expect(rest).toContain("egress");
  // from shortly before the word, at a word boundary, and not cut at the end
  const lead = rest.indexOf("egress");
  expect(lead).toBeGreaterThan(0);
  expect(lead).toBeLessThanOrEqual(40);
  expect(text[text.length - rest.length - 1]).toBe(" ");

  // a word near the start needs no cut, and a text without one is shown whole
  expect(descriptionLine(text, ["sends"])).toBe(text);
  expect(descriptionLine(text, ["zzz"])).toBe(text);
  expect(descriptionLine(text, [])).toBe(text);
  // the first of the words that occur decides
  expect(descriptionLine(text, ["rest", "egress"])).toBe(line);
  // a word that is later than a space-less stretch is still shown
  const glued = `${"x".repeat(100)}needle tail`;
  expect(descriptionLine(glued, ["needle"])).toContain("needle");
});

test("a word found only in a second name is shown beside the primary name", () => {
  const proxy = entry({
    names: ["Proxy URL", "Ignore system proxy"],
    keyPath: "providers[].proxy",
    descriptions: ["Route requests through this proxy server"],
  });
  const second = (query: string) =>
    searchSettings([proxy], query).hits[0]?.secondMatched;

  expect(second("ignore")).toBe("Ignore system proxy");
  expect(second("ignore proxy")).toBe("Ignore system proxy");
  // found in the primary name, or in the key, the second name stays out
  expect(second("proxy")).toBeNull();
  expect(second("url")).toBeNull();
  expect(second("providers")).toBeNull();
  expect(second("url ignore")).toBe("Ignore system proxy");
  // not found in a name at all
  expect(second("route")).toBeNull();
  // an entry with one name never has one
  const single = entry({ names: ["Max turns"] });
  expect(searchSettings([single], "turns").hits[0]!.secondMatched).toBeNull();
});

// ---- the index, on the served schemas

test("the index lists the tabs, their blocks and their fields in the drawer's order", () => {
  const index = indexOf(AGENT_SCHEMA);
  const sections = deriveSettingsSections(AGENT_SCHEMA);
  const label = (id: string) => sections.find((s) => s.id === id)!.label;

  // the tabs, in the rail's order, each followed by what it holds
  const tabs = index.filter((e) => e.kind === "section");
  expect(tabs.map((e) => e.section)).toEqual(sections.map((s) => s.id));
  expect(tabs.map((e) => e.names[0])).toEqual(sections.map((s) => s.label));
  expect(tabs.every((e) => e.anchor === null && e.row === null)).toBe(true);
  const firstOf = (id: string) => index.findIndex((e) => e.section === id);
  const starts = sections.map((s) => firstOf(s.id));
  expect(starts).toEqual([...starts].sort((a, b) => a - b));
  for (const s of sections) {
    const own = index.filter((e) => e.section === s.id);
    expect(own[0]!.kind).toBe("section");
    const at = index.indexOf(own[0]!);
    expect(index.slice(at, at + own.length)).toEqual(own);
  }

  // an object tab with blocks: the blocks stand in the order the tab declares
  // them, and each field of a block after it and before the next one
  const agent = index.filter((e) => e.section === "agent");
  const declared = objectSectionGroups("agent")!;
  const blocks = agent.filter((e) => e.kind === "group");
  expect(blocks.map((e) => e.anchor)).toEqual(
    declared.map((g) => groupAnchor("agent", g.id)),
  );
  expect(blocks.map((e) => e.names)).toEqual(declared.map((g) => [g.legend]));
  declared.forEach((g, i) => {
    const from = agent.indexOf(blocks[i]!);
    const to =
      i + 1 < blocks.length ? agent.indexOf(blocks[i + 1]!) : agent.length;
    for (const path of g.paths!) {
      const field = only(index, fieldAnchor("agent", path));
      expect(agent.indexOf(field), path).toBeGreaterThan(from);
      expect(agent.indexOf(field), path).toBeLessThan(to);
      expect(field.where, path).toEqual([label("agent"), g.legend]);
    }
    // inside a block the fields keep the schema's order
    const agentSchema = AGENT_SCHEMA.properties!["agent"]!;
    expect(
      agent.slice(from + 1, to).map((e) => e.keyPath),
      g.id,
    ).toEqual(
      entriesInSchemaOrder(
        agentSchema.properties!,
        agentSchema["x-coddy-property-order"],
      )
        .map(([k]) => k)
        .filter((k) => g.paths!.includes(k))
        .map((k) => `agent.${k}`),
    );
  });
  const turns = only(index, "agent:max_turns");
  expect(turns.keyPath).toBe("agent.max_turns");
  expect(turns.kind).toBe("field");
  expect(turns.names).toEqual([
    schemaFieldLabel(
      "agent",
      "max_turns",
      AGENT_SCHEMA.properties!["agent"]!.properties!["max_turns"]!.title,
      "max_turns",
    ),
  ]);

  // a section's own switch opens its form, outside every block
  const supervisor = index.filter((e) => e.section === "supervisor");
  expect(supervisor.slice(0, 3).map((e) => e.anchor)).toEqual([
    null,
    "supervisor:enable",
    "group:supervisor:checks",
  ]);

  // an object field is an entry of its own, followed by its children
  const limits = only(index, "tools:output_limits");
  const read = only(index, "tools:output_limits.read");
  expect(index.indexOf(read)).toBe(index.indexOf(limits) + 1);
  expect(read.where).toEqual([...limits.where, limits.names[0]]);
  expect(read.keyPath).toBe("tools.output_limits.read");
  expect(read.list).toBeNull();

  // a list of plain values is one entry; a map is one entry
  expect(entriesAt(index, "tools:command_allowlist")).toHaveLength(1);
  expect(entriesAt(index, "tools:http_request.default_headers")).toHaveLength(
    1,
  );

  // a tab with a list of objects: the list, then its children, which carry the
  // list and no count; the entry frame is no entry
  const groupsList = only(index, "gateways:telegram.user_groups");
  const groupName = only(index, "gateways:telegram.user_groups.name");
  expect(groupName).toMatchObject({
    list: groupsList.anchor,
    fallbackAnchor: groupsList.anchor,
    keyPath: "gateways.telegram.user_groups[].name",
  });
  expect(groupName.where).toEqual([...groupsList.where, groupsList.names[0]]);
  expect(groupsList.list).toBeNull();
  expect(index.filter((e) => e.anchor === groupsList.anchor).length).toBe(1);
  // a nested object inside an entry keeps the list
  const relay = indexOf(RELAY_SCHEMA);
  expect(only(relay, "swarm:upstreams.dial.proxy")).toMatchObject({
    list: "swarm:upstreams",
    keyPath: "swarm.upstreams[].dial.proxy",
  });

  // a list tab: each row that can be opened is an entry, then its own fields,
  // row after row
  const providers = index.filter((e) => e.section === "providers");
  const rows = providers.filter((e) => e.kind === "row");
  expect(rows.map((e) => e.row)).toEqual([
    "openai",
    "anthropic",
    "neuraldeep",
    "codex",
    "devin",
  ]);
  for (const row of rows) {
    const own = providers.filter((e) => e.row === row.row);
    expect(own[0]).toBe(row);
    expect(
      providers.slice(
        providers.indexOf(row),
        providers.indexOf(row) + own.length,
      ),
    ).toEqual(own);
  }

  // the blocks a tab draws by hand are landmarks, in the order they are
  // declared: those above the tab's forms come first, those below come last
  for (const id of [
    "appearance",
    "sessions_manager",
    "skills",
    "mcp_servers",
  ]) {
    const marks = (place: "before" | "after") =>
      SETTINGS_LANDMARKS.filter(
        (l) => l.section === id && l.row === undefined && l.place === place,
      ).map((l) => landmarkAnchor(l.section, l.id));
    const entries = index.filter((e) => e.section === id).slice(1);
    const above = marks("before");
    const below = marks("after");
    expect(
      entries.slice(0, above.length).map((e) => e.anchor),
      id,
    ).toEqual(above);
    expect(
      entries.slice(entries.length - below.length).map((e) => e.anchor),
      id,
    ).toEqual(below);
    expect(
      entries
        .slice(above.length, entries.length - below.length)
        .some((e) => e.kind === "landmark"),
      id,
    ).toBe(false);
  }
  expect(only(index, "landmark:skills:auto_discovery").keyPath).toBe(
    "skills.auto_discovery",
  );
  // the field a landmark draws by hand is not indexed a second time
  expect(entriesAt(index, "skills:auto_discovery")).toEqual([]);
  expect(only(index, "landmark:mcp_servers:discovery").keyPath).toBe(
    "mcp.project_trust",
  );

  // a folded block carries no description; the Prompts tab nests its children
  expect(
    only(index, "group:providers:advanced", "openai").descriptions,
  ).toEqual([]);
  expect(
    index.filter((e) => e.section === "system").map((e) => e.anchor),
  ).toEqual(
    expect.arrayContaining([
      "group:system.prompts:prompts",
      "system.prompts:dir",
    ]),
  );

  // nothing is indexed twice
  expect(new Set(index.map((e) => e.id)).size).toBe(index.length);
  const places = index.map((e) => `${e.section}|${e.row ?? ""}|${e.anchor}`);
  expect(new Set(places.filter((p) => !p.endsWith("|null"))).size).toBe(
    places.filter((p) => !p.endsWith("|null")).length,
  );

  // text is folded once, when the index is built
  expect(turns.hay.own).toBe(
    normalizeSearchText(`${turns.names[0]}\nagent.max_turns`),
  );

  // a relay has its own tabs and no Sessions
  expect(
    indexOf(RELAY_SCHEMA)
      .filter((e) => e.kind === "section")
      .map((e) => e.section),
  ).toEqual(["appearance", "swarm", "logger"]);
  // without a schema only the tabs that need none are there, holding nothing
  // but their hand-drawn blocks
  const bare = buildSettingsSearchIndex({
    sections: deriveSettingsSections(null),
    schema: null,
    doc: {},
  });
  expect(
    bare.filter((e) => e.kind === "section").map((e) => e.section),
  ).toEqual(["appearance", "sessions_manager"]);
  expect(bare.every((e) => e.kind === "section" || e.kind === "landmark")).toBe(
    true,
  );
});

test("a field of a provider or model row is indexed once per row, under that row's name", () => {
  const index = indexOf(AGENT_SCHEMA);
  const rows = ["openai", "anthropic", "neuraldeep", "codex", "devin"];

  expect(
    index
      .filter((e) => e.kind === "row" && e.section === "providers")
      .map((e) => [e.row, e.names, e.anchor, e.where]),
  ).toEqual(
    rows.map((name) => [
      name,
      [name],
      "row:providers",
      [translate("settings.section.providers.label")],
    ]),
  );
  for (const name of rows) {
    const proxy = only(index, "providers:proxy", name);
    expect(proxy.where.slice(0, 2)).toEqual([
      translate("settings.section.providers.label"),
      name,
    ]);
    // the row's own fields come after the row, before the next row
    const own = index.filter(
      (e) => e.section === "providers" && e.row === name,
    );
    expect(own[0]!.kind).toBe("row");
    expect(own.every((e) => e.row === name)).toBe(true);
  }
  expect(
    index.filter((e) => e.anchor === "providers:proxy").map((e) => e.row),
  ).toEqual(rows);

  // models: one row each, the id's two names, no description
  const models = ["demo/qwen3-27b", "demo/llama-3"];
  expect(
    index
      .filter((e) => e.anchor === "models:model")
      .map((e) => [e.row, e.names, e.descriptions]),
  ).toEqual(
    models.map((m) => [
      m,
      [
        schemaFieldLabel(
          "models",
          "model",
          AGENT_SCHEMA.properties!["models"]!.items!.properties!["model"]!
            .title,
          "model",
        ),
        translate("settings.field.provider"),
      ],
      [],
    ]),
  );
  expect(only(index, "models:model", "demo/llama-3").keyPath).toBe(
    "models[].model",
  );

  // what an override declares is what is indexed
  expect(only(index, "providers:proxy", "openai").names).toEqual([
    schemaFieldLabel(
      "providers",
      "proxy",
      AGENT_SCHEMA.properties!["providers"]!.items!.properties!["proxy"]!.title,
      "proxy",
    ),
    translate("settings.providerProxy.ignoreSystem"),
  ]);
  expect(only(index, "providers:proxy", "openai").descriptions).toHaveLength(2);
  expect(only(index, "providers:api_base", "codex")).toMatchObject({
    names: [translate("codexAuth.fieldLabel")],
    descriptions: [translate("codexAuth.description")],
  });
  expect(only(index, "providers:api_base", "neuraldeep")).toMatchObject({
    names: [expect.any(String), translate("neuralDeepAuth.fieldLabel")],
    descriptions: [
      translate("neuralDeepApiBase.description"),
      translate("neuralDeepAuth.description"),
    ],
  });
  expect(only(index, "providers:api_base", "openai").names).toHaveLength(1);
  expect(only(index, "gateways:telegram.proxy")).toMatchObject({
    names: [
      expect.any(String),
      translate("settings.providerProxy.ignoreSystem"),
    ],
    descriptions: [
      expect.any(String),
      translate("settings.gatewayProxy.ignoreSystemDesc"),
    ],
  });
  expect(entriesAt(index, "gateways:pachca.proxy")).toHaveLength(1);

  // a field the row does not draw is not indexed for it
  const has = (anchor: string, row: string) =>
    entriesAt(index, anchor).some((e) => e.row === row);
  expect(has("providers:api_key", "openai")).toBe(true);
  expect(has("providers:api_key", "codex")).toBe(false);
  expect(has("providers:api_key_command", "codex")).toBe(false);
  expect(has("providers:api_key_command", "devin")).toBe(true);
  expect(has("providers:usage_limits_panel", "openai")).toBe(false);
  expect(has("providers:usage_limits_panel", "anthropic")).toBe(false);
  for (const name of ["neuraldeep", "codex", "devin"]) {
    expect(has("providers:usage_limits_panel", name)).toBe(true);
  }

  // the Models block of the provider form is a landmark of every row
  expect(
    index
      .filter((e) => e.anchor === "landmark:providers:models")
      .map((e) => e.row),
  ).toEqual(rows);

  // the rows are the form's own: a type edited in the document is the type
  // the index reads
  const doc = configFromSchema(AGENT_SCHEMA);
  (doc["providers"] as Record<string, unknown>[])[0]!["type"] = "codex";
  const edited = indexOf(AGENT_SCHEMA, doc);
  expect(
    entriesAt(edited, "providers:api_key").some((e) => e.row === "openai"),
  ).toBe(false);
});

test("a row without a name and rows that share a name are left out, so no result opens the wrong row", () => {
  const rowsOf = (index: readonly SettingsSearchEntry[], section: string) => [
    ...new Set(
      index
        .filter((e) => e.section === section && e.row !== null)
        .map((e) => e.row),
    ),
  ];
  const defaults = indexOf(AGENT_SCHEMA);

  // the document has an unnamed provider and two called "shared"
  expect(rowsOf(defaults, "providers")).toEqual([
    "openai",
    "anthropic",
    "neuraldeep",
    "codex",
    "devin",
  ]);
  expect(defaults.some((e) => e.row === "" || e.row === "shared")).toBe(false);
  expect(
    defaults.some(
      (e) => e.where.includes("shared") || e.names.includes("shared"),
    ),
  ).toBe(false);

  // a name is the row's trimmed label, as the list trims it
  const doc = configFromSchema(AGENT_SCHEMA);
  const providers = doc["providers"] as Record<string, unknown>[];
  providers[5]!["name"] = "  padded  ";
  providers[6]!["name"] = "twin";
  providers[7]!["name"] = " twin ";
  const trimmed = indexOf(AGENT_SCHEMA, doc);
  expect(rowsOf(trimmed, "providers")).toEqual([
    "openai",
    "anthropic",
    "neuraldeep",
    "codex",
    "devin",
    "padded",
  ]);
  expect(only(trimmed, "providers:proxy", "padded").where[1]).toBe("padded");

  // the same holds for logical models
  const models = doc["models"] as Record<string, unknown>[];
  models[1]!["model"] = "demo/qwen3-27b";
  expect(rowsOf(indexOf(AGENT_SCHEMA, doc), "models")).toEqual([]);
  models[1]!["model"] = "   ";
  expect(rowsOf(indexOf(AGENT_SCHEMA, doc), "models")).toEqual([
    "demo/qwen3-27b",
  ]);

  // a document without a list has no rows, and no field of one
  const bare = indexOf(AGENT_SCHEMA, {});
  expect(
    bare.some((e) => e.section === "providers" && e.kind !== "section"),
  ).toBe(false);
  expect(entriesAt(bare, "agent:max_turns")).toHaveLength(1);
});

test("the rows key changes with the names and types of the rows and with nothing else", () => {
  const doc = configFromSchema(AGENT_SCHEMA);
  const key = settingsIndexRowsKey(doc);
  const edit = (change: (d: SettingsDoc) => void) => {
    const copy = structuredClone(doc);
    change(copy);
    return settingsIndexRowsKey(copy);
  };
  const providers = (d: SettingsDoc) =>
    d["providers"] as Record<string, unknown>[];

  expect(key).toBe(settingsIndexRowsKey(structuredClone(doc)));
  expect(edit((d) => (providers(d)[0]!["name"] = "renamed"))).not.toBe(key);
  expect(edit((d) => (providers(d)[0]!["type"] = "codex"))).not.toBe(key);
  expect(edit((d) => (providers(d)[1]!["api_key"] = "other"))).toBe(key);
  expect(
    edit(
      (d) => ((d["models"] as Record<string, unknown>[])[0]!["model"] = "x/y"),
    ),
  ).not.toBe(key);
  expect(
    edit((d) => {
      (d["agent"] as Record<string, unknown>)["max_turns"] = 1;
    }),
  ).toBe(key);
  expect(settingsIndexRowsKey({})).toBe(settingsIndexRowsKey({ agent: {} }));
});

test("values are never indexed: a key, a token or a URL of the document finds nothing", () => {
  const doc = configFromSchema(AGENT_SCHEMA);
  const index = indexOf(AGENT_SCHEMA, doc);
  const relay = indexOf(RELAY_SCHEMA);

  for (const [name, value] of Object.entries(SECRET_VALUES)) {
    const pieces = [value, value.slice(0, 14), value.split("//").pop()!];
    for (const piece of pieces) {
      expect(found(index, piece), `${name}: ${piece}`).toEqual([]);
      expect(found(relay, piece), `${name}: ${piece}`).toEqual([]);
    }
    const lowered = value.toLowerCase();
    for (const e of [...index, ...relay]) {
      const text = [e.hay.own, e.hay.where, e.hay.descriptions].join("\n");
      expect(text.includes(lowered), `${name} in ${e.id}`).toBe(false);
    }
  }
  // the names of the secrets are there
  expect(only(index, "tools:websearch.brave_api_key").names[0]).toBeTruthy();
  expect(found(index, "brave").length).toBeGreaterThan(0);

  // the one value that is read: a row's name
  expect(found(index, "qwen3-27b")).toContain("demo/qwen3-27b");
  expect(found(index, "anthropic")).toContain("anthropic");
});

test("the index speaks the page's language and keeps the config key as YAML spells it", () => {
  setLocale("ru");
  const agentSchema = AGENT_SCHEMA.properties!["agent"]!;
  expect(messagesRu["settings.schema.agent.max_turns.label"]).toBeTruthy();
  const index = indexOf(AGENT_SCHEMA);
  const sections = deriveSettingsSections(AGENT_SCHEMA);

  const russian = messagesRu["settings.schema.agent.max_turns.label"]!;
  const english = messagesEn["settings.schema.agent.max_turns.label"]!;
  expect(russian).not.toBe(english);
  const turns = only(index, "agent:max_turns");
  expect(turns.names).toEqual([russian]);
  expect(turns.keyPath).toBe("agent.max_turns");
  expect(turns.where[0]).toBe(translate("settings.section.agent.label"));
  expect(sections.find((s) => s.id === "agent")!.label).toBe(turns.where[0]);
  expect(turns.where[1]).toBe(translate("settings.group.agent.turn"));
  expect(only(index, "logger:levels.component").keyPath).toBe(
    "logger.levels[].component",
  );
  expect(agentSchema.properties!["max_turns"]).toBeTruthy();

  // found by the Russian name and by the key, not by the English name
  expect(found(index, russian)).toContain(russian);
  expect(found(index, "agent.max_turns")).toContain(russian);
  // the English name is not on the page, so it finds nothing by itself; the
  // key keeps its English words, so "max turns" still reaches the same field
  const memoryRoot = only(index, "memory:dir");
  expect(memoryRoot.names).toEqual([
    messagesRu["settings.schema.memory.dir.label"],
  ]);
  expect(
    searchSettings(
      index,
      messagesEn["settings.schema.memory.dir.label"]!,
    ).hits.map((h) => h.entry.id),
  ).not.toContain(memoryRoot.id);
  expect(found(index, english)).toContain(russian);

  // the landmarks speak it too
  expect(only(index, "landmark:appearance:language").names).toEqual([
    messagesRu["appearance.languageLabel"],
  ]);
  setLocale("en");
  expect(only(indexOf(AGENT_SCHEMA), "agent:max_turns").names).toEqual([
    english,
  ]);
});

test("every landmark heading and every drawn text is a key of the default dictionary", () => {
  const pairs = SETTINGS_LANDMARKS.map((l) => `${l.section}/${l.id}`);
  expect(new Set(pairs).size).toBe(pairs.length);
  const sectionIds = new Set(
    deriveSettingsSections(AGENT_SCHEMA).map((s) => s.id),
  );

  for (const l of SETTINGS_LANDMARKS) {
    expect(sectionIds.has(l.section), `${l.section} is a tab`).toBe(true);
    expect(SETTINGS_TEXTS[l.heading], l.id).toBeTruthy();
    expect(landmarkAnchor(l.section, l.id)).toBe(
      `landmark:${l.section}:${l.id}`,
    );
  }
  for (const [id, text] of Object.entries(SETTINGS_TEXTS)) {
    const keys = Object.values(text) as string[];
    expect(keys.length, id).toBeGreaterThan(0);
    for (const key of keys) {
      expect(messagesEn[key], `${id}: ${key} (en)`).toBeTruthy();
      expect(messagesRu[key], `${id}: ${key} (ru)`).toBeTruthy();
    }
  }
  // The builder is only a string: a drawn anchor the index lacks is caught by
  // the comparison with the drawing, not by a throw in the component.
  expect(landmarkAnchor("nope", "theme")).toBe("landmark:nope:theme");

  // the headings are drawn from the constants: label and the heading's (i)
  const index = indexOf(AGENT_SCHEMA);
  const discovery = only(index, "landmark:mcp_servers:discovery");
  expect(discovery.names).toEqual([translate("mcp.discovery.legend")]);
  expect(discovery.descriptions).toEqual([
    translate("mcp.discovery.description"),
  ]);
  expect(only(index, "landmark:appearance:theme").descriptions).toEqual([]);
  expect(only(index, "landmark:skills:installed").descriptions).toEqual([
    translate("skills.install.cliHint"),
  ]);
});

test("the Notifications landmark is left out where the tab draws none", () => {
  expect(
    entriesAt(indexOf(AGENT_SCHEMA), "landmark:appearance:notifications"),
  ).toHaveLength(1);

  launchAsTelegram();
  const telegram = indexOf(AGENT_SCHEMA);
  expect(entriesAt(telegram, "landmark:appearance:notifications")).toEqual([]);
  expect(
    telegram.filter((e) => e.section === "appearance").map((e) => e.anchor),
  ).toEqual([
    null,
    "landmark:appearance:theme",
    "landmark:appearance:language",
  ]);
});

// ---- cost

test("a large document builds its index and answers a query in a measured time", () => {
  const doc = configFromSchema(AGENT_SCHEMA);
  const providerRow = (doc["providers"] as Record<string, unknown>[])[0]!;
  const modelRow = (doc["models"] as Record<string, unknown>[])[0]!;
  const types = ["openai", "anthropic", "neuraldeep", "codex", "devin"];
  doc["providers"] = Array.from({ length: 40 }, (_, i) => ({
    ...providerRow,
    name: `provider-${i}`,
    type: types[i % types.length],
  }));
  doc["models"] = Array.from({ length: 200 }, (_, i) => ({
    ...modelRow,
    model: `provider-${i % 40}/model-${i}`,
  }));

  const t0 = performance.now();
  const index = indexOf(AGENT_SCHEMA, doc);
  const built = performance.now() - t0;

  const queries = ["proxy", "provider-7 proxy", "max turns", "zzzz", "a"];
  const timings = queries.map((query) => {
    const start = performance.now();
    const result = searchSettings(index, query);
    return {
      query,
      ms: Number((performance.now() - start).toFixed(2)),
      shown: result.hits.length,
      more: result.more,
    };
  });
  // eslint-disable-next-line no-console
  console.log(
    `settings search: ${index.length} entries built in ${built.toFixed(1)} ms`,
    JSON.stringify(timings),
  );

  // sanity only; the suite asserts no timing
  expect(index.length).toBeGreaterThan(40 * 8 + 200 * 9);
  expect(timings[0]!.shown).toBeGreaterThan(0);
  expect(timings[3]).toMatchObject({ shown: 0, more: 0 });
});
