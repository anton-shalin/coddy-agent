/**
 * Dictionary keys of the names and descriptions a settings tab draws outside
 * the schema: an override's texts and the headings of the blocks drawn by hand.
 * The drawing and the settings search both read them from here.
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

/** What an override draws in a field's box, in the page's language. */
export type OverrideTexts = {
  /** The name of the control a search focuses; the result's title. */
  primary: string;
  primaryDesc?: string | undefined;
  /** A second name drawn in the box (the proxy's switch, a model's Provider). */
  second?: string | undefined;
  secondDesc?: string | undefined;
};

/** The data-settings-anchor of a hand-drawn block. */
export function landmarkAnchor(section: string, id: string): string {
  return `landmark:${section}:${id}`;
}
