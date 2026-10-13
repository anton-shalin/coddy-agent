/**
 * Dictionary keys of the names and descriptions a settings tab draws outside
 * the schema: the texts an override puts in place of a field's own, and the
 * headings of the blocks a tab draws by hand. The drawing reads them from here
 * and so does the settings search index, so what is searched is what is shown.
 */
export type DrawnText = { label?: string; desc?: string };

export const SETTINGS_TEXTS = {
  proxyIgnoreSystem: {
    label: "settings.providerProxy.ignoreSystem",
    desc: "settings.providerProxy.ignoreSystemDesc",
  },
  gatewayProxyIgnoreSystem: {
    label: "settings.providerProxy.ignoreSystem",
    desc: "settings.gatewayProxy.ignoreSystemDesc",
  },
  neuralDeepApiBase: { desc: "neuralDeepApiBase.description" },
  neuralDeepAccount: {
    label: "neuralDeepAuth.fieldLabel",
    desc: "neuralDeepAuth.description",
  },
  codexAccount: {
    label: "codexAuth.fieldLabel",
    desc: "codexAuth.description",
  },
  modelProvider: { label: "settings.field.provider" },
  appearanceTheme: { label: "appearance.themeLabel" },
  appearanceLanguage: { label: "appearance.languageLabel" },
  appearanceNotifications: { label: "appearance.notificationsLabel" },
  sessionsManage: {
    label: "sessions.manage.legend",
    desc: "sessions.manage.lead",
  },
  mcpDiscovery: {
    label: "mcp.discovery.legend",
    desc: "mcp.discovery.description",
  },
  mcpServers: {
    label: "mcp.servers.legend",
    desc: "mcp.servers.description",
  },
  skillsAutoDiscovery: { label: "skills.autoDiscovery.legend" },
  skillsMarketplaces: {
    label: "skills.sources.legend",
    desc: "skills.sources.description",
  },
  skillsInstalled: {
    label: "skills.installed.legend",
    desc: "skills.install.cliHint",
  },
  subagentsCatalog: {
    label: "subagents.catalog.legend",
    desc: "subagents.catalog.description",
  },
  providerModels: { label: "settings.providerModels.legend" },
} as const satisfies Record<string, DrawnText>;

export type SettingsTextId = keyof typeof SETTINGS_TEXTS;

/**
 * The anchor (data-settings-anchor) of a hand-drawn block's root. A string and
 * nothing else, so the components that draw the block can import it without
 * pulling in the index that lists them; a drawn anchor the index lacks fails
 * the comparison of the drawing with the index.
 */
export function landmarkAnchor(section: string, id: string): string {
  return `landmark:${section}:${id}`;
}

/** The dictionary key of one part of a drawn text, undefined when it has none. */
export function settingsTextKey(
  id: SettingsTextId,
  part: "label" | "desc",
): string | undefined {
  const text: DrawnText = SETTINGS_TEXTS[id];
  return text[part];
}

/**
 * Where one slot of a box's names and descriptions comes from: the schema's
 * own text for the field ("schema"), or a constant of SETTINGS_TEXTS.
 */
export type TextRef =
  | "schema"
  | { text: SettingsTextId; part: "label" | "desc" };

/**
 * What a field box draws when an override replaces the default control: the
 * primary name (the result's title and the control a search focuses), its
 * description, and a second name with its own description (the proxy's
 * "Ignore system proxy", a model's "Provider").
 */
export type BoxTexts = {
  primary: TextRef;
  primaryDesc?: TextRef;
  second?: TextRef;
  secondDesc?: TextRef;
};

/** BoxTexts resolved to the strings of the page's language. */
export type ResolvedBoxTexts = {
  primary: string;
  primaryDesc?: string;
  second?: string;
  secondDesc?: string;
};

/** ResolvedBoxTexts of a box that draws a second name: the proxy's switch, a model's Provider. */
export type ResolvedBoxTextsWithSecond = ResolvedBoxTexts & { second: string };

export function hasSecondName(
  texts: ResolvedBoxTexts,
): texts is ResolvedBoxTextsWithSecond {
  return texts.second !== undefined;
}
