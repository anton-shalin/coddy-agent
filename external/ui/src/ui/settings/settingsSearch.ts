/**
 * Settings search: the matching, the order of the results and the index of
 * everything the Settings drawer draws. Pure and synchronous.
 *
 * The index is built by walking the served schema with SchemaForm's own layout
 * (layoutSchemaForm), the per-tab declarations the tabs pass to their forms,
 * and the texts the overrides put in a box (overrideTexts), so a name or a
 * description is searched exactly as the drawing shows it. Values of the
 * document are never read, except the names of the rows of the two lists
 * (LLM providers, Logical models), which the list itself draws.
 */
import {
  defaultForSchema,
  entriesInSchemaOrder,
  fieldAnchor,
  groupAnchor,
  isScalarItem,
  layoutSchemaForm,
  type JsonSchema,
  type SchemaFormGroup,
} from "./SchemaForm";
import { schemaFieldDesc, schemaFieldLabel } from "./schemaI18n";
import {
  modelFormGroups,
  objectSectionGroups,
  overrideTexts,
  providerFormGroups,
  resolveBoxTexts,
  sessionsFormGroups,
  systemPartGroups,
} from "./SettingsSection";
import {
  ARRAY_LABEL_FIELDS,
  MCP_SECTION_ID,
  SESSIONS_CONFIG_KEY,
  type SectionDescriptor,
} from "./settingsSections";
import { skillsFormGroups } from "./SkillsSection";
import { subagentsFormGroups } from "./SubagentsSection";
import {
  landmarkAnchor,
  settingsTextKey,
  type SettingsTextId,
} from "./settingsTexts";
import { translate } from "../i18n/i18n";
import { notificationsShown } from "../pwa/NotificationsSetting";

/** The most results shown; the rest are counted. */
export const SETTINGS_SEARCH_LIMIT = 50;
/** How much of a description shows before the word that matched. */
export const SNIPPET_LEAD = 40;

type EntryKind = "section" | "row" | "group" | "field" | "landmark";

export type SettingsSearchEntry = {
  id: string;
  kind: EntryKind;
  /** The id of the tab. */
  section: string;
  /** The trimmed name of the list row it stands in; unique and non-empty. */
  row: string | null;
  /** The anchor of its box (data-settings-anchor); null for a tab. */
  anchor: string | null;
  /** The anchor of the list of objects it stands in, or null. No count is kept. */
  list: string | null;
  /** That list's box, used only when the list draws no entry. */
  fallbackAnchor: string | null;
  /** names[0] is the primary: the result's title and the name of the control to focus. */
  names: string[];
  descriptions: string[];
  /** The names around it: the tab, the row, the blocks and objects it stands in. */
  where: string[];
  /** The dotted YAML path, as the documentation spells it. */
  keyPath: string | null;
  /** The texts, folded once (normalizeSearchText), each kept apart. */
  hay: { own: string; where: string; descriptions: string };
};

export type SettingsSearchHit = {
  entry: SettingsSearchEntry;
  /** 1: its own names or key; 2: needed the names around it; 3: needed a description. */
  tier: 1 | 2 | 3;
  /** A second name that holds a word neither the primary name nor the key holds. */
  secondMatched: string | null;
};

// ---- text

/** Lower case, and ё as е. Nothing else is folded. */
export function normalizeSearchText(text: string): string {
  return text.toLowerCase().replace(/ё/g, "е");
}

/** The words of a query: split at whitespace, folded, each once. */
export function searchWords(query: string): string[] {
  const words = normalizeSearchText(query)
    .split(/\s+/)
    .filter((w) => w !== "");
  return [...new Set(words)];
}

export type EntryParts = {
  id: string;
  kind: EntryKind;
  section: string;
  names: string[];
  row?: string | null | undefined;
  anchor?: string | null | undefined;
  list?: string | null | undefined;
  fallbackAnchor?: string | null | undefined;
  descriptions?: string[] | undefined;
  where?: string[] | undefined;
  keyPath?: string | null | undefined;
};

/** An entry with its texts folded: the one place that builds one. */
export function makeSearchEntry(parts: EntryParts): SettingsSearchEntry {
  const where = parts.where ?? [];
  const descriptions = parts.descriptions ?? [];
  const keyPath = parts.keyPath ?? null;
  return {
    id: parts.id,
    kind: parts.kind,
    section: parts.section,
    row: parts.row ?? null,
    anchor: parts.anchor ?? null,
    list: parts.list ?? null,
    fallbackAnchor: parts.fallbackAnchor ?? null,
    names: parts.names,
    descriptions,
    where,
    keyPath,
    hay: {
      own: normalizeSearchText(
        [...parts.names, ...(keyPath === null ? [] : [keyPath])].join("\n"),
      ),
      where: normalizeSearchText(where.join("\n")),
      descriptions: normalizeSearchText(descriptions.join("\n")),
    },
  };
}

// ---- matching

function tierOf(entry: SettingsSearchEntry, words: readonly string[]) {
  const { own, where, descriptions } = entry.hay;
  let tier: 1 | 2 | 3 = 1;
  for (const word of words) {
    if (own.includes(word)) {
      continue;
    }
    if (where.includes(word)) {
      tier = tier === 3 ? 3 : 2;
      continue;
    }
    if (descriptions.includes(word)) {
      tier = 3;
      continue;
    }
    return null;
  }
  return tier;
}

function secondNameFor(
  entry: SettingsSearchEntry,
  words: readonly string[],
): string | null {
  if (entry.names.length < 2) {
    return null;
  }
  const primary = normalizeSearchText(entry.names[0]!);
  const key = normalizeSearchText(entry.keyPath ?? "");
  const lone = words.filter((w) => !primary.includes(w) && !key.includes(w));
  if (lone.length === 0) {
    return null;
  }
  for (const name of entry.names.slice(1)) {
    const folded = normalizeSearchText(name);
    if (lone.some((w) => folded.includes(w))) {
      return name;
    }
  }
  return null;
}

// A tab, a block or a landmark leads the first tier; the rest follows.
const LEADS: ReadonlySet<EntryKind> = new Set(["section", "group", "landmark"]);

/**
 * The entries every word of the query occurs in, best first: those whose own
 * names or config key hold every word (tabs and blocks ahead), then those that
 * needed the names around them, then those that needed a description; the
 * drawer's order inside each. At most `limit` come back, `more` counts the rest.
 */
export function searchSettings(
  index: readonly SettingsSearchEntry[],
  query: string,
  limit: number = SETTINGS_SEARCH_LIMIT,
): { hits: SettingsSearchHit[]; more: number } {
  const words = searchWords(query);
  if (words.length === 0) {
    return { hits: [], more: 0 };
  }
  const matched: { entry: SettingsSearchEntry; tier: 1 | 2 | 3; at: number }[] =
    [];
  index.forEach((entry, at) => {
    const tier = tierOf(entry, words);
    if (tier !== null) {
      matched.push({ entry, tier, at });
    }
  });
  const rank = (m: (typeof matched)[number]) =>
    m.tier === 1 && !LEADS.has(m.entry.kind) ? 1 : 0;
  matched.sort((a, b) => a.tier - b.tier || rank(a) - rank(b) || a.at - b.at);
  const shown = matched.slice(0, Math.max(0, limit));
  return {
    hits: shown.map(({ entry, tier }) => ({
      entry,
      tier,
      secondMatched: secondNameFor(entry, words),
    })),
    more: matched.length - shown.length,
  };
}

/**
 * The ranges of `text` (in its own indices) that hold a word, merged. A text
 * whose lower case has the length of the text maps one to one; a character
 * whose lower case does not (İ) is folded on its own to keep the ranges true.
 */
function wordRanges(
  text: string,
  words: readonly string[],
): [number, number][] {
  const needles = words.filter((w) => w !== "");
  if (text === "" || needles.length === 0) {
    return [];
  }
  let folded = normalizeSearchText(text);
  let starts: number[] | null = null;
  let ends: number[] | null = null;
  if (folded.length !== text.length) {
    starts = [];
    ends = [];
    folded = "";
    let at = 0;
    for (const ch of text) {
      const piece = normalizeSearchText(ch);
      for (let k = 0; k < piece.length; k++) {
        starts.push(at);
        ends.push(at + ch.length);
      }
      folded += piece;
      at += ch.length;
    }
  }
  const found: [number, number][] = [];
  for (const word of needles) {
    for (let from = folded.indexOf(word); from >= 0; ) {
      const to = from + word.length;
      found.push(starts && ends ? [starts[from]!, ends[to - 1]!] : [from, to]);
      from = folded.indexOf(word, from + 1);
    }
  }
  found.sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const merged: [number, number][] = [];
  for (const range of found) {
    const last = merged[merged.length - 1];
    if (last !== undefined && range[0] <= last[1]) {
      last[1] = Math.max(last[1], range[1]);
    } else {
      merged.push([range[0], range[1]]);
    }
  }
  return merged;
}

/** The text in parts, the ones holding a word of the query marked. */
export function markWords(
  text: string,
  words: readonly string[],
): { text: string; hit: boolean }[] {
  if (text === "") {
    return [];
  }
  const parts: { text: string; hit: boolean }[] = [];
  let at = 0;
  for (const [from, to] of wordRanges(text, words)) {
    if (from > at) {
      parts.push({ text: text.slice(at, from), hit: false });
    }
    parts.push({ text: text.slice(from, to), hit: true });
    at = to;
  }
  if (at < text.length) {
    parts.push({ text: text.slice(at), hit: false });
  }
  return parts;
}

/**
 * A description as a result's line: whole when its first matching word is near
 * the start, else from shortly before that word, at a word boundary, with an
 * ellipsis where the start was cut.
 */
export function descriptionLine(
  text: string,
  words: readonly string[],
): string {
  const first = wordRanges(text, words)[0];
  if (first === undefined || first[0] <= SNIPPET_LEAD) {
    return text;
  }
  let start = first[0] - SNIPPET_LEAD;
  const gap = /\s/.exec(text.slice(start, first[0]));
  if (gap !== null) {
    start += gap.index + 1;
  }
  return `…${text.slice(start)}`;
}

// ---- landmarks

export type SettingsLandmark = {
  /** The tab. */
  section: string;
  id: string;
  /** The heading and the heading's (i): a constant of SETTINGS_TEXTS. */
  heading: SettingsTextId;
  keyPath?: string;
  /** Whether the block stands above or below the tab's generated forms. */
  place: "before" | "after";
  /** The block's own predicate: false where the tab does not draw it. */
  shown?: () => boolean;
  /** Present for a block of a list's row form; says whether this row draws it. */
  row?: (row: Record<string, unknown>) => boolean;
};

/**
 * The blocks a tab draws by hand. Each is its heading and the heading's (i),
 * rendered from the same constant; what is below the heading (filters, tables,
 * editor cards, the install box) is not a setting and is not indexed.
 */
function landmark(
  section: string,
  id: string,
  heading: SettingsTextId,
  place: "before" | "after",
  more: Partial<Pick<SettingsLandmark, "keyPath" | "shown" | "row">> = {},
): SettingsLandmark {
  return { section, id, heading, place, ...more };
}

export const SETTINGS_LANDMARKS: readonly SettingsLandmark[] = [
  landmark("appearance", "theme", "appearanceTheme", "before"),
  landmark("appearance", "language", "appearanceLanguage", "before"),
  landmark("appearance", "notifications", "appearanceNotifications", "before", {
    shown: notificationsShown,
  }),
  landmark("sessions_manager", "manage", "sessionsManage", "after"),
  landmark(MCP_SECTION_ID, "discovery", "mcpDiscovery", "before", {
    keyPath: "mcp.project_trust",
  }),
  landmark(MCP_SECTION_ID, "servers", "mcpServers", "before"),
  landmark("skills", "auto_discovery", "skillsAutoDiscovery", "before", {
    keyPath: "skills.auto_discovery",
  }),
  landmark("skills", "marketplaces", "skillsMarketplaces", "after"),
  landmark("skills", "installed", "skillsInstalled", "after"),
  landmark("subagents", "catalog", "subagentsCatalog", "after"),
  landmark("providers", "models", "providerModels", "after", {
    row: () => true,
  }),
];

// ---- the index

type PlainObject = Record<string, unknown>;

function asObject(v: unknown): PlainObject {
  return v !== null && typeof v === "object" && !Array.isArray(v)
    ? (v as PlainObject)
    : {};
}

function asArray(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}

function text(v: unknown): string {
  return v === undefined || v === null ? "" : String(v);
}

/** A row's label as the list trims it (SettingsArraySection). */
function rowLabel(row: unknown, field: string): string {
  return text(asObject(row)[field]).trim();
}

/**
 * What the rows of the two lists are, for the one thing the index reads from
 * the document: the trimmed names, and a provider's type, which decides the
 * fields its form draws. The index is rebuilt when this changes and not when
 * anything else does.
 */
export function settingsIndexRowsKey(doc: PlainObject): string {
  return JSON.stringify(
    Object.entries(ARRAY_LABEL_FIELDS).map(([key, field]) =>
      asArray(doc[key]).map((row) => [
        rowLabel(row, field),
        key === "providers" ? text(asObject(row)["type"]) : "",
      ]),
    ),
  );
}

type Form = {
  /** The dictionary domain and the anchor prefix of its fields. */
  domain: string;
  /** What the config key of a field starts with ("agent", "providers[]"). */
  keyRoot: string;
  schema: JsonSchema;
  groups: SchemaFormGroup[] | undefined;
};

type Walk = {
  out: SettingsSearchEntry[];
  section: SectionDescriptor;
  form: Form;
  row: string | null;
};

function entryId(parts: {
  kind: EntryKind;
  section: string;
  row: string | null;
  anchor: string | null;
}): string {
  return [parts.kind, parts.section, parts.row ?? "", parts.anchor ?? ""].join(
    "|",
  );
}

function push(
  out: SettingsSearchEntry[],
  parts: Omit<EntryParts, "id">,
): SettingsSearchEntry {
  const entry = makeSearchEntry({
    ...parts,
    id: entryId({
      kind: parts.kind,
      section: parts.section,
      row: parts.row ?? null,
      anchor: parts.anchor ?? null,
    }),
  });
  out.push(entry);
  return entry;
}

/** A field to index and where it stands: what the recursion hands to its children. */
type FieldFrame = {
  walk: Walk;
  name: string;
  schema: JsonSchema;
  /** The dictionary path: the anchor and the override are looked up by it. */
  path: string;
  /** The dotted YAML path, as the documentation spells it. */
  keyPath: string;
  /** The object that holds the field's value. */
  parent: PlainObject;
  /** The names around it. */
  where: string[];
  /** The anchor of the list of objects it stands in, or null. */
  list: string | null;
};

function walkField(frame: FieldFrame): void {
  const { walk, name, schema, path, keyPath, parent, where, list } = frame;
  const { form, section } = walk;
  const box = overrideTexts(form.domain, path, parent);
  if (box === "hidden") {
    return;
  }
  let names: string[];
  let descriptions: string[];
  if (box !== null) {
    const r = resolveBoxTexts(box, form.domain, path, schema);
    names = r.second === undefined ? [r.primary] : [r.primary, r.second];
    descriptions = [r.primaryDesc, r.secondDesc].filter(
      (d): d is string => !!d,
    );
  } else {
    names = [schemaFieldLabel(form.domain, path, schema.title, name)];
    const desc = schemaFieldDesc(form.domain, path, schema.description);
    descriptions = desc ? [desc] : [];
  }
  const anchor = fieldAnchor(form.domain, path);
  push(walk.out, {
    kind: "field",
    section: section.id,
    row: walk.row,
    anchor,
    list,
    fallbackAnchor: list,
    names,
    descriptions,
    where,
    keyPath,
  });
  if (box !== null) {
    return;
  }
  const inside = [...where, names[0]!];
  // A nested object draws its fields in its own fieldset.
  if (schema.type === "object" && schema.properties) {
    const held = parent[name];
    const value =
      held !== null && typeof held === "object" && !Array.isArray(held)
        ? (held as PlainObject)
        : asObject(defaultForSchema(schema));
    for (const [k, sub] of entriesInSchemaOrder(
      schema.properties,
      schema["x-coddy-property-order"],
    )) {
      walkField({
        ...frame,
        name: k,
        schema: sub,
        path: `${path}.${k}`,
        keyPath: `${keyPath}.${k}`,
        parent: value,
        where: inside,
      });
    }
    return;
  }
  // A list of objects draws one frame per entry, with no box of its own: its
  // fields are the targets, one entry for all the frames, which carries the
  // list. How many frames there are is read from the drawing, never kept here.
  const items = schema.items;
  if (
    schema.type === "array" &&
    items !== undefined &&
    !isScalarItem(items) &&
    items.type === "object" &&
    items.properties !== undefined
  ) {
    const value = asObject(defaultForSchema(items));
    for (const [k, sub] of entriesInSchemaOrder(
      items.properties,
      items["x-coddy-property-order"],
    )) {
      walkField({
        ...frame,
        name: k,
        schema: sub,
        path: `${path}.${k}`,
        keyPath: `${keyPath}[].${k}`,
        parent: value,
        where: inside,
        list: anchor,
      });
    }
  }
}

function walkForm(walk: Walk, parent: PlainObject, where: string[]): void {
  const { form, section } = walk;
  const key = (k: string) => `${form.keyRoot}.${k}`;
  for (const node of layoutSchemaForm(form.schema, form.groups)) {
    if (node.kind === "field") {
      walkField({
        walk,
        name: node.key,
        schema: node.schema,
        path: node.key,
        keyPath: key(node.key),
        parent,
        where,
        list: null,
      });
      continue;
    }
    const g = node.group;
    push(walk.out, {
      kind: "group",
      section: section.id,
      row: walk.row,
      anchor: groupAnchor(form.domain, g.id),
      names: [g.legend],
      // A folded block draws no description.
      descriptions: !g.collapsible && g.description ? [g.description] : [],
      where,
    });
    const inside = [...where, g.legend];
    for (const [k, sub] of node.fields) {
      walkField({
        walk,
        name: k,
        schema: sub,
        path: k,
        keyPath: key(k),
        parent,
        where: inside,
        list: null,
      });
    }
  }
}

function landmarkEntries(
  out: SettingsSearchEntry[],
  section: SectionDescriptor,
  landmarks: readonly SettingsLandmark[],
  row: string | null,
): void {
  for (const l of landmarks) {
    if (l.shown !== undefined && !l.shown()) {
      continue;
    }
    const label = settingsTextKey(l.heading, "label");
    const desc = settingsTextKey(l.heading, "desc");
    push(out, {
      kind: "landmark",
      section: section.id,
      row,
      anchor: landmarkAnchor(l.section, l.id),
      names: [label === undefined ? l.id : translate(label)],
      descriptions: desc === undefined ? [] : [translate(desc)],
      where: row === null ? [section.label] : [section.label, row],
      keyPath: l.keyPath,
    });
  }
}

type TabForms = {
  /** The generated forms, each with the value it is drawn over. */
  forms: { form: Form; value: PlainObject }[];
  /** A list tab: the form each of its rows opens. */
  rows?: Form;
};

/**
 * What a tab generates from the schema; null when it draws a notice instead
 * (its section of the schema is missing), so it holds nothing to find.
 */
function tabForms(
  section: SectionDescriptor,
  props: Record<string, JsonSchema>,
  doc: PlainObject,
): TabForms | null {
  const key = section.schemaKey ?? section.id;
  const one = (
    domain: string,
    keyRoot: string,
    groups: SchemaFormGroup[] | undefined,
  ): TabForms | null => {
    const schema = props[keyRoot];
    return schema
      ? {
          forms: [
            {
              form: { domain, keyRoot, schema, groups },
              value: asObject(doc[keyRoot]),
            },
          ],
        }
      : null;
  };
  switch (section.kind) {
    case "object":
      return one(section.id, key, objectSectionGroups(key));
    case "skills":
      return one("skills", "skills", skillsFormGroups());
    case "subagents":
      return one("subagents", "subagents", subagentsFormGroups());
    case "sessions":
      // The table under the form is drawn with or without the section.
      return (
        one(SESSIONS_CONFIG_KEY, SESSIONS_CONFIG_KEY, sessionsFormGroups()) ?? {
          forms: [],
        }
      );
    case "group":
      return {
        forms: (section.childKeys ?? []).flatMap((ck) => {
          const schema = props[ck];
          return schema
            ? [
                {
                  form: {
                    domain: `system.${ck}`,
                    keyRoot: ck,
                    schema,
                    groups: systemPartGroups(ck, schema),
                  },
                  value: asObject(doc[ck]),
                },
              ]
            : [];
        }),
      };
    case "array": {
      const items = props[key]?.items;
      return items
        ? {
            forms: [],
            rows: {
              domain: section.id,
              keyRoot: `${key}[]`,
              schema: items,
              groups:
                key === "providers"
                  ? providerFormGroups()
                  : key === "models"
                    ? modelFormGroups()
                    : undefined,
            },
          }
        : null;
    }
    default:
      return { forms: [] };
  }
}

/** The rows of a list tab a name can open: trimmed, non-empty and unique. */
function openableRows(
  list: unknown[],
  labelField: string,
): { label: string; row: PlainObject }[] {
  const labels = list.map((row) => rowLabel(row, labelField));
  const counts = new Map<string, number>();
  for (const label of labels) {
    counts.set(label, (counts.get(label) ?? 0) + 1);
  }
  return list.flatMap((row, i) =>
    labels[i] !== "" && counts.get(labels[i]!) === 1
      ? [{ label: labels[i]!, row: asObject(row) }]
      : [],
  );
}

/**
 * The index of the Settings drawer: a tab, then what it draws in the order it
 * draws it. `sections` are the rail's tabs, `schema` the one the drawer holds,
 * `doc` the settings document (read only for the rows of the two lists).
 */
export function buildSettingsSearchIndex(input: {
  sections: readonly SectionDescriptor[];
  schema: JsonSchema | null;
  doc: PlainObject;
}): SettingsSearchEntry[] {
  const out: SettingsSearchEntry[] = [];
  const props = input.schema?.properties ?? {};

  for (const section of input.sections) {
    push(out, {
      kind: "section",
      section: section.id,
      names: [section.label],
      descriptions: section.description ? [section.description] : [],
    });
    const generated = tabForms(section, props, input.doc);
    if (generated === null) {
      continue;
    }
    const tab = [section.label];
    const landmarks = SETTINGS_LANDMARKS.filter(
      (l) => l.section === section.id,
    );
    const blocks = (place: "before" | "after") =>
      landmarks.filter((l) => l.row === undefined && l.place === place);

    landmarkEntries(out, section, blocks("before"), null);
    for (const { form, value } of generated.forms) {
      walkForm({ out, section, form, row: null }, value, tab);
    }
    const key = section.schemaKey ?? section.id;
    for (const { label, row } of generated.rows
      ? openableRows(asArray(input.doc[key]), section.labelField ?? "")
      : []) {
      push(out, {
        kind: "row",
        section: section.id,
        row: label,
        anchor: `row:${section.id}`,
        names: [label],
        where: tab,
      });
      walkForm({ out, section, form: generated.rows!, row: label }, row, [
        ...tab,
        label,
      ]);
      landmarkEntries(
        out,
        section,
        landmarks.filter((l) => l.row?.(row) === true),
        label,
      );
    }
    landmarkEntries(out, section, blocks("after"), null);
  }
  return out;
}
