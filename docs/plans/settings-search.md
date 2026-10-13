# Settings search design

**Date:** 2026-10-13. The design was revised five times after review:
- a technical and a UX review on 2026-10-12;
- a cross-review on 2026-10-13;
- its second, third and final rounds, also on 2026-10-13.

Appendix A logs every finding.

**Status:** implemented in the pull request that adds this record. Section 17 lists where the implementation departs from the text above and why; the rest is the design as it was decided.

**Scope:** the web UI Settings drawer (`#/settings`) on an agent and on a relay, and the Telegram Mini App that embeds it. The change touches SPA code, adds one Go test, and checks a copy of the served form schema into `internal/config/testdata/`.

**Base:** `origin/main` at `49b895e7`. Every `file:line` cited here was re-read on that commit; the branch was later rebased onto `1f9f285a`, which touched none of the cited files.

## 1. Context

The Settings drawer has 18 tabs on an agent:

- Appearance, Sessions;
- LLM providers, Logical models;
- ReAct loop, Session goal, Context compaction, Memory;
- Command safety, Tools and permissions, MCP servers, Skills, Subagents, Hooks;
- Scheduler, Gateways;
- Logger, Prompts.

On a relay it has three: Appearance, Swarm relay, Logger.

Several things make a setting hard to find:

- The dictionaries carry 187 translated field labels and 195 descriptions under `settings.schema.*`.
- Two tabs are master-detail lists. Their fields exist only inside an open row.
- Some fields sit in a folded block: the provider form's **Advanced settings**.
- Override components draw names of their own: **Ignore system proxy**, **ChatGPT account**, **NeuralDeep account**, and **Provider** in a model row.
- Several blocks are hand-written and API-driven: MCP servers, the Skills marketplaces and installed list, the Subagents definitions, the session table, and the Appearance blocks.

Today, to change a setting the operator has to know which tab, which row and which block hold it. Nothing in the drawer searches.

The page already has everything a search needs, so no server help is required:

- **The form's schema.** `settingsConfigStore.ts` reads it once from `GET /coddy/config/schema`. The server returns `config.UISchemaJSON` or `config.RelayUISchemaJSON` verbatim (`internal/configapi/configapi.go:79-88`). The schema text is English, and the route takes no language parameter.
- **Both dictionaries,** bundled. They translate every field through `schemaFieldLabel` and `schemaFieldDesc` (`settings/schemaI18n.ts`).
- **The tabs,** from `deriveSettingsSections` (`settings/settingsSections.ts:131`).
- **The document,** whose `providers[].name` and `models[].model` label the list rows.

## 2. Goals

1. One search field in the Settings drawer finds any setting of any tab: a field, a block, a tab, or a row of a list.
2. Choosing a result takes the operator to the setting:
   - the tab opens;
   - the row opens;
   - a folded block unfolds;
   - the setting's own box scrolls into view and is ringed;
   - the focus lands on the setting.
3. Matching is plain and predictable. Each result shows why it matched.
4. It works in every registered locale, on a wide drawer, a tablet and a phone, on a relay's own Settings, and in the Telegram Mini App.
5. The drawing and the index read the same declarations, and one test compares them box by box on the served schema.
6. Searching never touches the form's document or its unsaved state.

## 3. Non-goals

- Searching records, such as MCP servers, installed skills, subagent definitions, stored sessions, or a provider's listed models. The rows of LLM providers and Logical models are the one exception (5.4).
- Searching inside the operational controls of a hand-written block: the session table's filters, the MCP toolbar, the Skills install box. Such a block is found by its heading (5.1).
- Searching values. That covers current values, enum choices and placeholders. A secret is never searched.
- Fuzzy matching, relevance ranking, synonyms, or typo tolerance.
- A command palette, a global shortcut that opens Settings, a query in the address, or a query kept after the drawer closes.
- A config key, an HTTP route, a server index, or any Go production code.
- Guarding tab-local drafts on a tab switch. That is a separate issue (7.7).
- The console.

## 4. User stories

1. **Wide screen.** I press `/` and type `proxy`. Every proxy setting shows. Enter on "Proxy URL - LLM providers › openai › Advanced settings" does three things: it opens the openai row, unfolds **Advanced settings**, and rings the proxy setting with the cursor in the URL input.
2. **Config file reader.** I type `max_turns` or `agent.max_turns` and land on **Max turns**.
3. **Russian page.** `таймаут` finds the Russian names of the timeout fields. `timeout_ms` finds the same fields by their config key.
4. **Many models.** I search `qwen temp` and change the first model's **Temperature**. I click the field or press `/` from outside a text field: the results are back, with that model selected. ArrowDown, then Enter, takes me to the next model.
5. **Phone or Telegram Mini App.** The field stands over the tiles, and the results take their place. Choosing a result rings the setting and does not raise the keyboard. Back first clears a query whose results show, then goes back as before.
6. **Relay.** The same field searches the relay's Swarm relay and Logger settings.
7. **Unsaved edits.** I search, jump, and keep editing. **Unsaved changes** and the Save highlight stay.

## 5. What the search looks at

### 5.1 Entries and their boxes

The index is a flat list in the drawer's order. Every entry except a tab corresponds to a box the drawer draws (8.2):

| Kind | Example | Source | Box |
|---|---|---|---|
| tab | **Memory** | `deriveSettingsSections`, with the tile blurb as its second line | none: the tab's top |
| row | **openai** (LLM providers) | `providers[].name` / `models[].model`, trimmed as the list trims them (`SettingsArraySection.tsx:97`) | the named row form |
| block | **Retries** (ReAct loop) | the `SchemaFormGroup` legends the tabs pass | the group fieldset |
| field | **Max turns** | every field `SchemaField` draws, including object and list fieldsets | `.settings-field` |
| landmark | **MCP discovery** | a hand-drawn block, declared in 8.1 | the block's root |

**Rows.** The fields of the two list tabs are indexed once per row (5.4).

**Fields inside an inline list of objects.** These are fields such as a Telegram user group's name or a web search engine's fields.

- Each such field is indexed once, and remembers the list it belongs to.
- Its box is repeated: every entry of the list draws it under the same anchor.
- The index records no count. How many boxes a list draws is read from the drawing itself (8.3), so adding or removing an entry never makes the index stale.
- The reveal takes the first such box in document order, unfolding it if it is folded (7.6). It falls back to the list's own box only when the list draws no entry.
- An entry's frame, the unlabelled per-entry fieldset (`SchemaForm.tsx:290-297`), is not a target and has no box. Its children are the targets.

**Landmarks.** A landmark is its heading and the (i) of that heading. The heading is the block root's `legend`, or for an Appearance block its `.appearance-section-label`. What the block holds below the heading is not indexed: filters, toolbars, tables, editor cards, and the Skills install box. The session table's Archive and Source filters (`sessions/SessionsManager.tsx:618-643`), for example, are operational controls, not settings.

### 5.2 What is matched

All of this is matched as the page draws it, in its current language:

- **Own names.**
  - A field's names are those drawn in its box. The **primary** name comes first: the schema label, or what an override declares as primary (8.1). Second names follow, such as **Ignore system proxy** in a proxy box, or **Provider** in a model's id box.
  - A row's name is the row name.
  - A block's or a landmark's name is its heading.
- **Config key.** The dotted YAML path. Two landmarks also carry one: Skill auto-discovery (`skills.auto_discovery`) and MCP discovery (`mcp.project_trust`).
- **Surrounding names.** The tab, the row, and the legends of the blocks the entry stands in.
- **Descriptions.** For a field: every (i) text drawn in its box. For a block or a landmark: its heading's (i).

### 5.3 Languages (decided)

The search matches the page's language plus the config key, and not other locales.

- A match has to be shown marked, and other-language text is not on screen.
- The config key carries the English vocabulary that `config.yaml`, `coddy -t`, the docs and the `configure-coddy` skill print.
- Where a Russian entry is missing, `schemaFieldLabel` falls back to the English title. The search reads the same text.

### 5.4 Rows of LLM providers and Logical models (decided)

There is one entry per (row, field). The search indexes only rows the address can reach. A row opens by its name (`#/settings/models?id=<name>`), and a form that is already open stays open while the address names it (`SettingsArraySection.tsx:125-160`, the early return at `:143`). So:

- **A row without a name is not indexed.**
- **Rows that share a name are not indexed, none of them.** They can exist only as unsaved edits, because the server refuses two providers of one name or two models of one id (`internal/config/resolve.go:272`, `:284`).
- **A field the row does not draw is not indexed for it.** Visibility comes from `overrideTexts`, which the overrides read themselves (8.1).

### 5.5 Never indexed

- **Values.** The only values read are the two lists' row labels, which the list renders anyway. Secrets, URLs, prompts, and header names and values never reach the index. Their names do.
- **Keys the form hides,** listed in `uiHiddenConfigKeys` (`internal/config/ui_schema.go:853-859`).
- **Records,** and the operational controls of landmarks.
- **What a surface does not draw.** In Telegram, the Notifications block renders nothing (`pwa/NotificationsSetting.tsx:54`). The index uses the same `notificationsShown()`.

## 6. Matching and order

1. The query is split into words at whitespace. Case does not matter, and `ё` matches `е`. Nothing else is folded.
2. Every word has to occur, as a substring, in the own names, the config key, the surrounding names or the descriptions.
3. Results fall into three tiers. Each tier keeps the drawer's order.
   1. Every word is in the own names or the config key. Tabs, blocks and landmarks come first, then rows and fields.
   2. Some word needed the surrounding names.
   3. Some word needed the descriptions.
4. At most 50 results show (`SETTINGS_SEARCH_LIMIT`), followed by a line counting the rest. This follows the shape of `settings/installableMatches.ts:14`.
5. There is no debounce. Text is normalized once, when the index is built. Build and query times are measured on a document with 40 providers and 200 models and reported in the PR. The test suite asserts no timing.

## 7. UX

### 7.1 Placement

**Wide drawer** (`min-width: 1200px`):
- The field sits at the top of the section rail, inside its 8px inset (`.settings-nav-wrap`, 200px wide, `styles.css:10267`).
- The results take the section content's place.
- The rail shows no active tab while results show.

**Narrow shell** (`max-width: 1199px`):
- The field stands over the tile grid, inside the 14px inset.
- The results take the tiles' place.
- With a tab open, the head's back arrow leads back to the field, and so does `/`.

**Why the content column, and not a dropdown under the field?** The documentation reader and the swarm screen use a dropdown, but this field sits in a 200px rail. A result row needs a name, where it lives, and a config key. A dropdown that narrow is unreadable, and a wider one would cover the content column anyway, over a form that may hold unsaved edits.

**Set aside:** a band across the whole drawer under its head. It costs a row of height on every view.

### 7.2 The query's life

- **Typing** shows the results.
- **Choosing a result** hides the results and keeps the query. It remembers the chosen result and issues a new reveal request with its own sequence number. Choosing the same result again reveals it again.
- **Clicking or tapping the field,** or pressing `/` outside a text field, shows the results again with the chosen one highlighted.
- **A rail click or a tile tap** hides the results and keeps the query.
- **Closing the drawer** forgets the query.
- **The query lives in a store the drawer owns.** `Settings` re-renders only when the results start or stop showing, so typing never re-renders the tab under them.

### 7.3 Keyboard

| Key | Where | What it does |
|---|---|---|
| `/` | Settings, focus outside a text field, no dialog open | Puts the cursor in the field and shows its results. On a narrow shell with a tab open, it goes back to the tiles and focuses the field once it is drawn. |
| ArrowDown / ArrowUp | the field, results showing | Moves the highlighted result, wrapping around, and scrolls it into view (`block: "nearest"`). |
| Enter | the field, results showing | Opens the highlighted result. |
| Escape | Settings, results showing | Clears the query, wherever the focus is. |
| Escape | the field, results hidden, a query in it | Clears the query. |
| Escape | anywhere else | Takes the drawer's usual step. |

- **`/` follows the documentation reader and the swarm screen** (`docs/DocsView.tsx:233-252`, `swarm/SwarmView.tsx:184-199`). It checks `e.key === "/"` and does nothing in these cases:
  - Ctrl, Meta or Alt is held;
  - the event is defaultPrevented;
  - the focus is in a text field;
  - a modal dialog (`[aria-modal="true"]`) is open.
- **No two `/` handlers are live together.** The rail keeps one screen at a time, and the swarm screen at a relay's home unmounts while Settings is open (`App.tsx:7675`).
- **Escape on results showing** is the first step of the Settings screen's Escape chain (`Settings.tsx:352-361`). Telegram's Back dispatches an Escape at the focused element (`telegram/backButton.ts:50`), so it takes the same step.
- **The field claims Escape** in its own `onKeyDown` while it holds a query.
- **During composition, Enter and Escape belong to the input method.**
- **`aria-activedescendant` stays valid.** When the results change, the highlight stays on the chosen result if it is still among them, and otherwise moves to the first. `aria-activedescendant` names an option only while that option exists.

### 7.4 Results in place of the sections, and the tab under them

| | Results in place of the sections | Every tab's form filtered in place |
|---|---|---|
| Rows of providers and models | each (row, field) is a result | a field exists only in an open row |
| Folded blocks | unfolded on the way | every fold opens while typing |
| Hand-written tabs | a landmark each | cannot be filtered |
| Code touched | results, a reveal step, boxes | every renderer learns the query |
| Editing while searching | the form is left alone | fields vanish under the cursor |

**Decision: a result list.**

**The wide drawer: the tab under the results.** The tab is hidden with `display: none`, not unmounted. Its form drafts survive a query cleared without choosing: an MCP JSON being edited, a waiting device sign-in, an unnamed map row, the Skills install query, and fold states.

`display: none` also hides any `position: fixed` element inside the tab. Only portals and document- or window-level listeners escape the hidden tab.

**The sweep.** These are every portal and every document- or window-level key or pointer listener in the import closure of `settings/Settings.tsx`, re-read on `49b895e7`:

| Where | What | While results hide its tab |
|---|---|---|
| `settings/FieldHint.tsx:86`, `:99-102`, `:172` | Escape through `useEscapeCloses`; scroll, resize, mousedown and touchstart while open; the tip portalled to `<body>`. A tip pinned by a touch survives blur (`:124-136`). | The tip is drawn through `PanePortal`, so it is not drawn. Its open state is kept. Its close listeners are paused while the pane is hidden, and it is placed again when shown. |
| `settings/SkillsSection.tsx:327` | Escape through `useEscapeCloses` for the install results. The results themselves are inline. | `useEscapeCloses` does not listen. The results are hidden with the tab, and the query is kept. |
| `sessions/SessionTagEditor.tsx:226`, `:84` | The tag editor, portalled to `<body>`. Its click-catcher closes it on a mouse press. Its Escape lives on its input. | Drawn through `PanePortal`, so neither the editor nor its input exists. Its draft and its open state are kept, and it comes back when the results go. |
| `components/ConfirmDialog.tsx:130`, `:143` (used by the Sessions table) | A modal portal with a window key listener. | While it is open, nothing behind its backdrop can be reached, and `/` does nothing. Results cannot show over it, so it needs no handling. |
| `settings/Combobox.tsx:61` | mousedown on `document`, only while its list is open. The list is inline. | It is hidden with the tab. A mousedown elsewhere folds it, as today. No handling is needed. |
| `settings/SettingsNav.tsx:40` | scroll on the rail element | Not in the tab. |
| `settings/Settings.tsx:352`, `:364` | the screen's Escape step and close guard | Settings' own. They carry the results step. |
| `nav/railEscape.ts:201`, `components/useEscapeCloses.ts:36` | the shared mechanisms | `useEscapeCloses` reads `PaneHidden`. |

**The mechanism: two chokepoints.** `PaneHidden` is a context the drawer sets to `true` around the hidden tab. Two shared pieces read it, and one component besides:
- `PanePortal` (new, `components/PanePortal.tsx`) draws its children into `<body>` through `createPortal`, and draws nothing while `PaneHidden` is true. `FieldHint` and `SessionTagEditor` draw their portals through it.
- `useEscapeCloses` does not listen while `PaneHidden` is true.
- `FieldHint` reads it for its own listeners and placement. Its close-on-scroll, resize and outside-pointer listeners (`FieldHint.tsx:88-109`) run only while its pane is shown, so scrolling the results cannot close a hidden tip. Its placement (`:51-83`) is measured again when the pane is shown.

**The rule:** while results show, the tab under them is hidden and deaf. Its portals are not drawn, its Escape layers do not listen, and its state (drafts, an open tip, an open tag editor) is kept.

**What is guaranteed, and what is not.**
- **Tested:** every layer in the sweep table behaves as the table says. One behavioural test renders each of them under a hidden pane (a pinned tip, an open tag editor, the Skills install results) and checks that it draws nothing in `<body>` and claims no Escape. For the pinned tip, it also scrolls the results before they go, then checks that the tip is still pinned and placed against its (i).
- **By construction:** any future portal drawn through `PanePortal`, and any future Escape layer built on `useEscapeCloses`.
- **Not guaranteed:** a future component under Settings that calls `createPortal` directly, or adds a `document` listener of its own. Catching that statically would take a TypeScript parser that follows imports, re-exports and dynamic imports and counts calls per owner and event. That is more machinery than the risk warrants. The `ui-spa` rule and `DESIGN.md` name the two chokepoints, and review is the check.

**The narrow shell.** No tab is mounted under the results. They replace the tiles of the grid view.

### 7.5 A result row

1. **The primary name.** If a matched word occurs only in a second name, ` · <second name>` is appended. Words are marked.
2. **Where it is,** joined by ` › `, then the config key in monospace. Muted, 12px.
3. **The description** (tier 3 only), starting at most 40 characters before the first word found there, with an ellipsis where cut and a two-line clamp.

The option's accessible name is lines 1 and 2. Line 3 is its description, through `aria-describedby`.

### 7.6 Going to a setting

1. **The results hide** and a reveal request is issued (7.2).
2. **The tab opens** through `selectSection` (`Settings.tsx:318-322`). A row opens through `setSettingsSectionHash(section, {item: row})`. Rows are unique by name (5.4).
3. **The drawer finds the mounted box.** It waits with a `MutationObserver` until the box is in the document, for at most 1 s in all (`REVEAL_WAIT_MS`), and gives up quietly if it never comes. A box that is mounted but hidden inside a folded block counts as found: a folded body stays mounted (`SchemaForm.tsx:787`).
   - Lookup compares `dataset` values as strings, never through a selector built from user text.
   - A row's box is looked up only inside the row form whose `dataset.settingsRow` equals the row.
   - A repeated box resolves to the first one in document order. Only when the list draws no entry does it resolve to the list's own box. A folded entry is not an empty list.
4. **A folded block unfolds, then the drawer waits for visibility.** If a folded `CollapsibleFieldset` (`SchemaForm.tsx:761`) stands above the box, the drawer dispatches a reveal event from the box, and the fieldset opens with an ordinary state update. The drawer then waits, within the same 1 s, until no ancestor of the box is `hidden`. Only then do the next steps run.
5. **The box scrolls** into the middle of its scroll area: `scrollIntoView({block: "center"})`, smooth unless `prefers-reduced-motion`.
6. **The box is ringed** for 1.6 s (`SEARCH_TARGET_MS`). Under reduced motion, the ring holds still.
7. **The focus moves:**
   - **A field, on a fine pointer:** the control whose accessible name equals the entry's **primary** name, from its `aria-label`, its `aria-labelledby`, or its `<label>`. Disabled controls and `.field-hint` are skipped.
     - **Proxy URL** focuses the URL input, not the **Ignore system proxy** switch drawn before it.
     - A model's id box focuses the id input, not the **Provider** combobox.
     - With no such control, or with the URL input disabled, the box itself is focused.
   - **A touch-only device** (`snapshotTouchOnly()`, `shellBreakpoint.ts:72`, `(any-hover: none) and (any-pointer: coarse)`), and every tab, row, block and landmark: the box takes the focus (`tabIndex={-1}`). No keyboard rises.
   - **The focused box always has an accessible name.**
     - A fieldset is named by its legend.
     - A row form is a `role="group"` named by its row.
     - Any other box gets `role="group"` and an `aria-label` with the primary name, for as long as it holds the focus.

### 7.7 Unsaved edits and tab-local state

- The document is read only for the row labels, and never written.
- A jump to another tab is a rail click: the draft lives in `Settings`.
- A query cleared without choosing shows the tab as it was (7.4).
- **Kept behaviour.** A result in another tab unmounts the open tab, as a rail click does today. State outside the document goes with it. That is a separate issue, drafted in the implementation plan, section 0.2.

### 7.8 The head while results show

While results show, the head reads **Settings** with no back arrow, even over an open row. The narrow shell's results stand on the grid view, whose head already reads **Settings**.

### 7.9 Empty, loading and failure states

| State | What shows |
|---|---|
| Empty field | The sections. |
| No match | "No matching settings; try other or fewer words". |
| More than 50 | 50 results, then "N more settings match - add a word to narrow the list". |
| First read on its way | The field is disabled. |
| First read failed | The field searches what is drawn without a schema. |
| A newer copy or a language switch | The index is rebuilt, and the query and results follow it. |

### 7.10 Accessibility

- **The field** follows the documentation search's pattern (`docs/DocsView.tsx:383-456`): `type="search"`, `role="combobox"`, `aria-autocomplete="list"`, `aria-expanded`, `aria-controls`, `aria-activedescendant` (7.3), the placeholder as its name, and a visible focus ring.
- **The results** form a named `role="listbox"`. Each option is a `button` with `role="option"`, `aria-selected` and `tabIndex={-1}`.
- **The count** is announced through a polite `role="status"`, debounced by 500 ms (`STATUS_DEBOUNCE_MS`).
- **After a jump,** the focus is on a named control or a named box (7.6).
- **Marks** use the documentation search's colours. The ring is never the only signal.
- **Result rows** are at least 44px tall on a touch shell.

### 7.11 Strings

The new strings, in every registered dictionary:

- the placeholder;
- the listbox name;
- the empty state;
- the "more" line, through `translatePlural`;
- the found count, through `translatePlural`.

The Russian wording rule holds. See the implementation plan, section 5.

## 8. One source of truth

### 8.1 Shared declarations that the drawing consumes

- **Layout.** `layoutSchemaForm(schema, groups)` is extracted from `SchemaForm.tsx:831-906`, in the same file. `SchemaForm` renders from it, and the index walks it. `entriesInSchemaOrder`, `isBlockField` and `isScalarItem` are exported.
- **Schema texts.** `schemaFieldLabel` and `schemaFieldDesc`.
- **Per-tab declarations, exported where they live.** Nothing moves, so the `DESIGN.md` and `ui-spa` lines that name these places stay true:
  - `objectSectionGroups`;
  - `USAGE_PANEL_PROVIDER_TYPES`;
  - `providerFormGroups()` and `modelFormGroups()` (`SettingsSection.tsx:559-595`);
  - the Storage group (`:361-367`);
  - the Prompts groups (`:654-660`);
  - `skillsFormGroups()`;
  - `subagentsFormGroups()`.
- **Override texts.** `overrideTexts(domain, path, parent)` lives beside the overrides and returns one of three things:
  - `"hidden"`: the field is not drawn;
  - `null`: the field is drawn as the schema says;
  - a box text descriptor `{primary, primaryDesc?, second?, secondDesc?}`. Each slot is either `"schema"` or a constant of `settingsTexts.ts`.

  **The drawing consumes the resolved descriptor, not a mirror of it.** The override functions call `overrideTexts`. A `"hidden"` result returns `false`. Otherwise they resolve the descriptor to strings and pass them as one `texts` prop:
  - `ProxySettingField` draws the URL row from `primary` / `primaryDesc` and the switch from `second` / `secondDesc`.
  - `ModelField` draws the id from `primary` and **Provider** from `second`. It draws no (i), so its descriptor has no description.
  - `CodexAuthField` draws `primary` / `primaryDesc`.
  - The NeuralDeep endpoint draws `primary` / `primaryDesc`, and `NeuralDeepAuthField` draws `second` / `secondDesc`.

  The index flattens the same resolved descriptor. The comparison (8.3) then only checks that each component places what it was given.
- **Drawn texts.** `settings/settingsTexts.ts` holds the dictionary keys of every name and description drawn outside the schema. `overrideTexts` uses them, and the landmark blocks render their heading and its (i) from them.
- **Landmarks.** Each is declared as `{tab, id, heading, keyPath?, place, shown?, row?}`.
  - `heading` is a `settingsTexts` constant, its label plus the heading's (i).
  - `shown` is the component's own predicate. For example, `notificationsShown()` is `!telegramLaunch()`, and `NotificationsSetting` returns `null` on it.
  - `row` says whether a list row draws the block.

| Tab | Landmark | Heading constant (label / heading's (i)) | Key |
|---|---|---|---|
| Appearance | Theme | `appearance.themeLabel` / - | |
| Appearance | Language | `appearance.languageLabel` / - | |
| Appearance | Notifications (not in Telegram) | `appearance.notificationsLabel` / - | |
| Sessions | Session management | `sessions.manage.legend` / `sessions.manage.lead` | |
| MCP servers | MCP discovery | `mcp.discovery.legend` / `mcp.discovery.description` | `mcp.project_trust` |
| MCP servers | MCP servers | `mcp.servers.legend` / `mcp.servers.description` | |
| Skills | Skill auto-discovery | `skills.autoDiscovery.legend` / - | `skills.auto_discovery` |
| Skills | Marketplaces | `skills.sources.legend` / `skills.sources.description` | |
| Skills | Installed skills | `skills.installed.legend` / `skills.install.cliHint` | |
| Subagents | Definitions | `subagents.catalog.legend` / `subagents.catalog.description` | |
| LLM providers, each row | Models | `settings.providerModels.legend` / - | |

- **Restated in the index:** the branching by tab kind, about twenty lines. The comparison reports any slip.

### 8.2 Every target is a real box

There is no `display: contents`, because such an element has no box to ring or focus.

- **A field.** `SchemaField` wraps what it draws in `div.settings-field[data-settings-anchor="<domain>:<path>"]`. The box is a block flex column with the parent's gap and `min-inline-size: 0`.
  - A fragment override, such as the proxy switch plus the URL row (`ProxySettingField.tsx:46`), gets one box.
  - **An entry frame of a list of objects gets no box.** That frame is the `SchemaField` rendered with `entryLabel` and the list's own `path` (`SchemaForm.tsx:352-360`, `:290-297`). Without this exception, list and frame would carry the same anchor.
  - A field that `overrideTexts` hides gets no box.
- **A group:** its fieldset, with `data-settings-anchor="group:<domain>:<id>"`.
- **A row form:** `.settings-detail`, with `data-settings-anchor="row:<domain>"`, `data-settings-row`, `role="group"` and `aria-label="<row name>"`. A row form draws its name nowhere as text: a model's id is split across two inputs (`ModelField.tsx:30,55`).
- **A landmark:** its root, with `data-settings-anchor="landmark:<tab>:<id>"`.

Anchors are built from schema keys and fixed ids only.

### 8.3 One exact comparison, on the served schema

**The served schema.**
- Checked copies of the served schemas live at `internal/config/testdata/ui_schema.json` (65 KB) and `ui_schema_relay.json` (15 KB). This follows the precedent of `internal/mention/testdata/grammar_cases.json`, which `skills/draftAt.test.ts` reads.
- `TestUISchemaTestdataIsCurrent` in `internal/config/jsondto_schema_test.go` fails when a copy is stale. It normalizes line endings first. To regenerate the copies:

  ```bash
  go test ./internal/config -run '^TestUISchemaTestdataIsCurrent$' -update
  ```

- The builders depend on no build tag and no environment.

**The comparison.** The comparison tests go into `SettingsSection.test.tsx`; the shared fixture is `settings/settingsServer.fakes.ts`. They render, for both forms (agent and relay), every tab and every row form. The document is built from the schema copy:
- the schema's defaults;
- one entry in every list of objects;
- a provider row of every type;
- two model rows.

The server is stubbed, so the API-driven tabs draw no record. The test then checks three things, exactly and in both directions:

1. **Boxes.** Every drawn anchor has an index entry, and every entry's anchor is drawn. The one exception is an entry inside a list of objects that draws no entry; there the list's own box stands for it.
   - An anchor outside any list of objects is drawn exactly once.
   - Inside a list of objects, the count is read from the drawing. In each drawn instance of the list's box, each child anchor of that list appears exactly as many times as that instance draws entry frames of its own; a nested list's frames are not counted. Nested lists are therefore counted per actual parent entry, and the index carries no count.
   - A duplicate anchor fails. That is how an entry frame that took a box would show.
2. **Names per box.** For a field box, these are the names drawn directly in it, excluding nested boxes: `legend`, `label`, `h1`-`h6`, `.settings-label`, and a switch's own `aria-label` when it overrides a state label. For a row box, it is the `aria-label`. For a block or landmark box, it is the heading only.

   They must equal the entry's names, compared as multisets. The markup keeps its visual order: the proxy switch before the URL row (`ProxySettingField.tsx:48-56`), and **Provider** before the id (`ModelField.tsx:39-51`). The entry lists its primary name first. That the primary name is the result's title, and names the control that takes the focus, is asserted separately in the drawer tests.
3. **Descriptions per box.** For a field: each (i) drawn directly in the box is opened and its tip read. For a block or landmark: the heading's (i) only. They must equal the entry's descriptions, compared as multisets.

**Extra cases.**
- One list of objects is drawn empty, and once with two entries. This checks the count read from the drawing and the fallback.
- An edit after the index was built: an entry is added to `logger.levels`, then removed. The index is not rebuilt for it, and the comparison and a reveal still hold.
- The whole comparison also runs in Russian, and inside a faked Telegram launch, where it finds no Notifications block and no Notifications entry.

**The mutation trio.** These prove the comparison works. Each must make it fail:
- a leaf entry deleted from the index;
- a stale entry added whose anchor is not drawn;
- an entry's primary name changed.

**A sanity check.** Searching each entry's primary name returns that entry, with no limit applied.

**Cost.** About 50 renders. The PR reports their time and the aggregate `vitest` and `go test ./external/ui` times before and after. If the run is too slow, the harness gets cheaper; coverage is not cut.

**What the comparison covers, and what it does not.**
- The schema-driven part is single-sourced. The drawing and the index read the same layout function, the same schema texts, and the same resolved override descriptors.
- What stays a mirror: the twenty lines of tab-kind branching in the index, and the landmark list. Every landmark block renders its heading from the same constant it is declared with. The comparison is what holds those two in step.

## 9. Edge cases

- **Rows renamed while a query is typed:** the index is rebuilt, keyed on the row labels and types.
- **The open row is the target:** the box is there at once.
- **Another row of the open list is the target:** the address changes, the form remounts (it is keyed by row), and the lookup searches only the new row's form.
- **Rows sharing a name:** not offered.
- **Inline lists:**
  - **Empty:** no repeated box, and the reveal falls back to the list.
  - **Two entries:** two boxes, and the reveal takes the first one in document order.
  - **Edited after the index was built:** nothing goes stale, because the index holds no count.
- **Maps:** one entry. The keys and values are values, not names.
- **Write-only secrets:** their names are searchable, their values never are.
- **Overrides:** the primary name decides the result title and the focus.
- **Telegram:** no Notifications block. Narrow-shell and touch rules apply.
- **Relay:** the index is built from the relay's schema.
- **Tabs a newer server adds:** indexed from the schema's text.
- **A whitespace-only query:** no search.
- **A language switch:** the index is rebuilt.
- **Long names on narrow phones:** they wrap. Nothing scrolls sideways.
- **`/` typed with Shift:** matched on `e.key`.
- **The address:** names only tabs and rows.

## 10. Surfaces

| Surface | Affected | How |
|---|---|---|
| Web UI, agent (`tags=http,ui`) | yes | field, results, reveal |
| A relay's own Settings (`tags=http,ui,swarm`, `external/swarm/settings.go:29`) | yes, same code | the relay's schema |
| A node through a relay | yes, same code | the node's schema |
| Telegram Mini App | yes, same code | narrow shell, touch focus, Back as Escape, no Notifications |
| Console, `coddy acp`, HTTP API | no | |

## 11. Visual spec

This section uses DESIGN.md terms. No new token or colour is introduced.

- **The field** (`.settings-search-input`) shares the documentation search's field rules (`styles.css:14774`), by adding the settings selectors to those rules.
  - A `/` hint shows on the wide drawer only.
  - On the wide drawer, the field is the first child of `.settings-nav-wrap`, padded `8px 8px 0`.
  - On the narrow shell, it sits over `.settings-tile-grid`, inside the 14px inset.
- **The results** (`.settings-search-results`, a `.settings-scroll` of its own) share the documentation hit's rules:
  - rows `.settings-hit` with padding `8px 10px` and a 10px radius;
  - hover at 6% text, and `.is-selected` at 14% accent with a 32% border;
  - the name at weight 500;
  - lines 2 and 3 at 12px and 58% text;
  - the config key in `ui-monospace` 11.5px with `overflow-wrap: anywhere`;
  - line 3 clamped to two lines;
  - `mark` as `.docs-hit-snippet mark`.
- **The setting's box** (`.settings-field`): `display: flex`, `flex-direction: column`, the parent's gap, `min-inline-size: 0`, a 10px radius. No padding and no margin, so every row keeps its height (`settingsDrawerRhythmCss.test.ts`).
- **The ring** (`.is-search-target`, for 1.6 s):
  - `box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 55%, transparent)`;
  - a background wash of `color-mix(in srgb, var(--accent) 10%, transparent)`;
  - fading over the last 0.6 s.

  Its reduced-motion rule joins the existing settings block (`styles.css:10535`), because `SwarmView.test.tsx:1103-1115` reads the stylesheet's last reduced-motion block.
- **The hidden pane:** `.settings-scroll[hidden] { display: none; }`.

## 12. What does not change, and the prompt cache

- **Config:** no config key, no `config.schema.json` change, no `UISchemaMap()` change, and nothing for `make site-schema`.
- **HTTP:** no route, request or response change. `openapi.go` is untouched.
- **Go:** no production code. The Go changes are tests: `TestUISchemaTestdataIsCurrent` with its copies, and the godog harness (`//go:build http && ui`).
- **The prompt cache.**
  - **Search interactions** issue no request a turn makes. They leave every session's system prompt and history bytes as they were.
  - **The documentation part of the change can reach sessions whose workspace is this repository**, but not at once.
    - `DESIGN.md` is a standing document (`internal/rules/project_docs.go:29,77`).
    - `.cursor/rules/ui-spa.mdc` is a scoped rule of the folder such a session reads (`.cursor/rules`, the first present).
    - A session reads both when it discovers its rules (`internal/session/rules_load.go:11-20`). It keeps the catalog it read (`internal/session/state.go:1906-1919`) until the catalog is replaced at its next rules generation: a compaction, a config reload, a workspace switch, or a restart. A new session reads them fresh.
    - Editing the files alone changes nothing in a running session. From its next generation on, the new `DESIGN.md` text is in its system prompt. The new `ui-spa` text reaches its history in the result of the first tool call of that generation that touches `external/ui/**` and activates the rule.
  - **`.claude/rules/ui-spa.md`** is Claude Code's copy and reaches no Coddy session.
  - `make test-cache` is unaffected.
- **CLI:** no flag or command change. The man page and the completions are untouched.

## 13. Testing strategy

**Core.** `settingsSearch.test.ts`, a new module with its own test:
- words, tiers, the limit, marks, the description line, the title's second name;
- values;
- rows: unnamed, sharing a name, unique;
- both languages;
- the large-document measurement.

**The exact comparison** (8.3) goes in `SettingsSection.test.tsx`.

**Boxes and the fold.** `SchemaForm.groups.test.tsx`. Its `blocks()` helper (`:59-70`) classifies a form's direct children. It unwraps a `.settings-field` to its first child and keeps every ordering assertion.

**Hidden panes.**
- `components/useEscapeCloses.test.tsx`: the paused Escape layer, and `PanePortal`'s own test;
- one behavioural test in `Settings.test.tsx`, parameterized over the layers of the sweep table.

**The drawer.** `Settings.test.tsx`, which the feature harness names:
- `/`, including narrow `/` after commit;
- typing and choosing, and the same choice twice;
- the provider fold, and another row;
- duplicates;
- the kept query;
- the focus rules, including Proxy URL;
- unsaved edits;
- Escape's steps;
- each layer of the sweep table under shown results (the parameterized test above);
- an inline list edited after the index was built;
- the head;
- Telegram Back;
- no match;
- the active option;
- the narrow shell, Russian, and a relay;
- the status debounce.

Repeated cases are parameterized (`test.each`): pointer and touch focus, the Escape steps, Russian and relay. One mounting helper in the fake sets up the server, the width, the touch state and the locale.

The shared fake server is `settings/settingsServer.fakes.ts`, extracted from `Settings.test.tsx`'s `stubServer` and extended.

**CSS.** `settingsFieldsetCss.test.ts`.

**Go.** `TestUISchemaTestdataIsCurrent`.

**BDD.** `features/web_ui_settings_search.feature`, through `external/ui/bdd_settings_search_test.go`. It covers the happy path only.

**Browser.** These are the only checks of real boxes, paint, bounds and focus. CI does not run them: the Tests on PR workflow runs `check:transcript`, `check:queue` and `check:files`, but not the Settings or Telegram scripts. They are run by hand, and the PR reports each command, engine and outcome.
- **The existing Settings script** gains a `search` scenario, run against an agent (`tags=http,ui`) and a relay's own page (`tags=http,ui,swarm`), in Chromium and WebKit, at 320 to 1280px, in English and Russian. It opens pages as `/?lang=<lang>#/settings`, because the locale is read from `location.search` (`i18n/uiLocale.ts:36`).
  - **Queries are config keys** (`max_turns`, `proxy`, `pairing`). They match in every locale (6).
  - **A result is picked by its anchor.** Each option carries `data-settings-target` and `data-settings-row`. The expected names are read from the page, never written into the script: the option's primary name is the name the focused control must carry. The Russian labels differ from the English ones (`messages/ru.ts:401`, `:480`).
  - **Moving between steps.** It moves the focus out of a text field before pressing `/`. On a narrow shell, it goes back to the tiles through the head's arrow before it reopens the field: after a jump the field is not drawn (`Settings.tsx:668-682`).
  - **The shared helper polls until three things hold at once, within its deadline:** the box carries `.is-search-target` and a computed ring; it lies inside the intersection of the viewport and its scroll area; and `document.activeElement` is exactly the expected element. A smooth scroll (7.6) may still be running when the ring appears, so bounds are never read once and judged.
- **The Telegram script** gains a `settings` scenario. It runs in a touch context, created for it, and asserts the touch-only media query before relying on it. It covers:
  - the touch reveal;
  - Back with the results reopened explicitly;
  - Back from a tab to the tiles, as its own step;
  - no Notifications result.
- **Both scripts share one assertion helper.**
- **Also run:** the chat-edge comparison of `.claude/rules/ui-verification.md`, and `webkit-scroll-check.mjs`.

## 14. Rejected alternatives

| Alternative | Why not |
|---|---|
| Server-side search, or fuzzy ranking | The data is already in the page. Both make the results unpredictable. |
| A hand-written settings list | A second source to keep in step. |
| Indexing by rendering every tab off-screen | It mounts the API-driven tabs, and only one row form renders at a time. |
| A command palette | Out of scope. |
| `Combobox` as the field | It picks a value; this field navigates. |
| Moving the per-tab declarations | Churn, and it makes the contract lines that name them stale. |
| Unmounting the tab under the results | It loses drafts for a search that ends without a choice. |
| `display: contents` anchors | They have no box to ring or focus. |
| Name-only coverage, and set-only box comparison | Each misses lost entries or duplicate anchors. |
| Opening the first of two same-named rows by index | It needs a second address channel, for a state the server refuses to save. |
| A hand-written fixture schema | A copy of the form, where the served one exists. |
| Indexing what a landmark holds below its heading | Its filters and toolbars are operations, not settings. |
| Components composing override texts alongside a mirrored descriptor | Two sources. The drawing consumes the resolved descriptor instead. |
| Box counts stored in the index | They go stale when a list is edited. The drawing is the count. |
| A static, parser-based inventory of every portal and listener under Settings | More machinery than the risk warrants. Two chokepoints and a behavioural test give a narrower, honest guarantee (7.4). |

## 15. Decisions

1. Other locales' names are not matched.
2. The tests use the checked copy of the served schema. **Maintainer veto point; stands.**
3. The field sits at the top of the rail, and at the top of the tiles.
4. At most 50 results show; the rest are counted.
5. Browser checks extend the existing Settings and Telegram scripts, share one helper, are run by hand, and are reported in the PR.
6. One entry per (row, field), for reachable rows only.
7. This document is committed as `docs/plans/settings-search.md`.
8. The query stays after a choice. Each choice is its own request.
9. While results show, the tab under them is hidden and deaf. Its portals are not drawn (`PanePortal`), its Escape layers do not listen (`useEscapeCloses`), and its state is kept. **Maintainer veto point; stands.**
10. Nothing moves. Declarations are exported where they are.
11. Tab-local drafts are a separate issue; this feature keeps today's behaviour. **Maintainer veto point; stands.**
12. The drawing consumes resolved override descriptors. Landmarks are a heading plus its (i). The comparison is exact: counts are read from the drawing, names and descriptions are compared as multisets, and a mutation trio proves it.
13. The script rename (`settings-unsaved-check.mjs` to `settings-check.mjs`) is a later cleanup, not part of this PR.
14. One feature PR. If the maintainer prefers smaller reviews, the behaviour-preserving refactors can land first as a separately scoped PR: the exports where they live, `layoutSchemaForm`, `overrideTexts` with the `texts` prop, and `settingsTexts.ts`.

## 16. Open questions for the maintainer

None remain open.

The three veto points (decisions 2, 9 and 11) are recorded as decisions. The maintainer can still reverse any of them.

## 17. Implementation notes

What the implementation does differently from the text above, and why. Each point is covered by the tests named in `DESIGN.md` (**Settings drawer**, **Search**).

1. **The component file is `settings/SettingsSearchView.tsx`.** `SettingsSearch.tsx` next to `settingsSearch.ts` resolves to the `.ts` module on a case-insensitive file system (macOS, Windows), so `tsc` fails there and the wrong module loads.
2. **`landmarkAnchor` is a plain string builder in `settingsTexts.ts`**, beside `SETTINGS_TEXTS`, and does not throw. In `settingsSearch.ts` it made every landmark component import the index, which imports `SettingsSection`, which imports those components: a cycle. A drawn anchor the index does not declare still fails the drawing comparison. `SETTINGS_LANDMARKS` stays in `settingsSearch.ts`. For the same reason the `BoxTexts` types live in `settingsTexts.ts`, and `SkillsSection.tsx` exports the name of its hand-drawn field (`SKILLS_HAND_DRAWN_FIELD`) for `overrideTexts`.
3. **Only what another file reads is exported.** `USAGE_PANEL_PROVIDER_TYPES` and `isBlockField` (8.1) stay private: the index reaches them through `overrideTexts` and `layoutSchemaForm`.
4. **A tab result has no box.** The reveal takes the tab's scroll area (`.settings-scroll[data-settings-tab]`) to its top and focuses it, named by the tab while it holds the focus, with no ring. Its option carries `data-settings-target="tab:<section>"`.
5. **A fold listens in a layout effect, and the reveal asks again.** In a real browser the reveal asked the fold to open in the mutation that mounted the box, before the fold's passive effect had attached its listener, so the fold never opened (jsdom did not show it). `CollapsibleFieldset` now listens in a layout effect, and the reveal dispatches the event again at every mutation while something above the box is hidden, then waits a frame before it scrolls and focuses.
6. **The theme landmark has a wrapper of its own.** The Appearance sheet's root also holds the Language and Notifications blocks, so the theme label and swatches are wrapped in a plain `div` that carries the anchor; the layout does not change.
7. **Small choices the design left open:** `/` also does nothing with the focus in a `<select>`; the polite status announces the shown results plus the counted rest; the ring's wash is an inset box-shadow of the same accent mix, so it fades with the ring; emptying the field (the native clear button of a search field included) clears the search like Escape; a click or focus in the field while the results show keeps the highlighted result; the highlight follows the pointer only when the pointer moves to another spot, because a browser reports a move at the same spot when new results come in under a resting pointer (the browser check found the highlight taken that way after a pasted query); on the narrow shell the results are put away when a tab opens under them (a deep link).
8. **Two dictionary keys went away.** Once the overrides draw their names from one descriptor, `settings.field.apiBaseFallback` and `settings.field.modelIdFallback` were read by nothing: `schemaFieldLabel` never returns an empty name.
9. **Test helpers are split by job.** `settingsServer.fakes.ts` holds the stub server (which fails a test on any route it does not serve), the schema copies, `configFromSchema` and the mount helpers; `settingsDrawing.fakes.ts` holds `drawnBoxes` and the comparison; `pinnedTip.fakes.ts` the pinned-tip helpers that `FieldHint.test.tsx` and the hidden-layer test share. `FieldHint` and `SessionTagEditor` also got unit tests of their own under a hidden pane, so their change was tested before the drawer existed.
10. **The browser checks.** The relay asks for a client token even on loopback, so the search check takes it from `CODDY_RELAY_TOKEN`. The Telegram check's stand had stopped opening the Mini App on `origin/main` (the bot answers `/app` only for its admins), so its configuration now names the stand's chat as an admin. At the maintainer's request the search check also runs in CI: with `CODDY_BIN` set it starts its own agent and relay, and the Tests on PR workflow runs it in Chromium. WebKit and the Telegram `settings` scenario stay manual. The search check also asserts the kept query, the order of the tiers, a reveal into a model row, the Escape order, the touch focus and the scroll position of the hidden tab.
11. **`revealWhenReady(root, entry, {touchOnly, reducedMotion})` returns its cancel and takes no `done` callback**; nothing needed one.
12. **The address names the setting the operator was taken to.** Section 3 lists "a query in the address" as a non-goal, and section 9 says the address names only tabs and rows. After the first demo the maintainer asked for an address that names the highlighted setting, so a link can be sent at once. The Settings address gains `field=<anchor>` beside `id=<row>`: `#/settings/<tab>?id=<row>&field=<anchor>`, with `id=` only for a row of LLM providers or Logical models, the anchor URL-encoded (a colon as `%3A`) and `history=1` kept last. It is written with `replaceState`, so it adds no history entry, and `appNavHrefSettingsSection` takes one `SettingsAddress` (`{item, field, historySidebar}`) instead of three positional options.
    - **Writing:** a choice that has a box (a field, a group, a block, a row) writes it; a tab result writes none.
    - **Dropping:** the address loses the setting when the operator goes elsewhere (a rail click or a tile tap, the head's back arrow, another row, closing the drawer), and a new choice replaces it.
    - **Opening:** an address that names a setting (a pasted link, a reload, a relay's own Settings, the Telegram Mini App, whose launch capture keeps it) opens the tab and the row through the route, and the drawer takes the operator to the setting as a choice does. The drawer finds the entry in the index by anchor, tab and row, and asks again when the index changes, because the first read may still be on its way. The address read back after a choice is not a second trip. An anchor the index does not know (a link written by another version) only opens the tab and the row.
    - **The non-goal still holds in its own words:** the address names the setting, never the query. To share a setting, choose its result and copy the address.
13. **A block of fields is a result that opens like a setting.** Section 5.1 already indexed groups for their legends. Choosing one now opens the tab and the row, unfolds the block when it is itself folded (a provider's Advanced settings), scrolls to it, rings the whole block and focuses the named block. The reveal treats a shut collapsible box like a hidden ancestor: a group chosen by its name is the fold, with its body hidden under it, and the event reaches it first. The comparison of the drawing with the index also checks that every legend, and every Appearance label, drawn in a tab is the heading of an indexed box; a mutation that drops a block's entry must fail it.
14. **The `/` badge shows on every width, while it is useful.** Section 11 says the hint shows on the wide drawer only. It now shows inside the field while the field is empty and does not have the focus, on every width (a narrow desktop window has a keyboard); it hides while the field has the focus or holds text, and a touch-only device never shows it. After a jump the focus sits in the revealed setting, so the badge advertises the way back to the field. The documentation reader and the swarm screen keep their own rules for their badges.
15. **The look of the reveal changed after the demo.** The ring replaces the box-shadow ring and wash of note 7. It is a 2px accent outline, 3px off the box so the switch and the label of a field never touch it, over a light accent wash (an inset shadow, so no ground of the box is painted over). Both come in over the first 8% of the 1.6 s, hold, and fade over the last 0.6 s, and under reduced motion they hold still. A box taller than its scroll area is aligned by its top instead of centred, so its name and its first fields show. Every box keeps a 12px scroll margin, so the ring is never cut at the edge of the scroll area. A box the reveal focused from the keyboard shows no second, browser focus ring once the ring has faded.
16. **The spec and the browser check grew with these.** `features/web_ui_settings_search.feature` has 12 scenarios; it adds a link, a group and the badge. The browser check additionally asserts the address with `field=`, opening a link, a search for a group and the badge.

17. **A result's config key has a line of its own.** Inline after the path (7.5), the key wrapped or not by the length of the path beside it, so the rows of one list had different shapes; on a line of its own every row looks the same.

## Appendix A. Review log

**Key:** T = technical review (2026-10-12), U = UX review (2026-10-12), C = cross-review (2026-10-13), X = its second round (2026-10-13), Y = its third round (2026-10-13), Z = its final round (2026-10-13).

| # | Finding | What changed | Status |
|---|---|---|---|
| T1 | Escape reaches the hidden tab's listeners | `PaneHidden` stops `useEscapeCloses`; results showing is the first Escape step (7.3, 7.4) | resolved |
| T2 | The narrow shell has its own rule; the row title shows over the results | 7.4, and a plain head (7.8) | resolved |
| T3 | The wrong control is focused | The control named like the primary name, else the box (7.6) | resolved |
| T4 | A move leaves contract text stale | No move (8.1) | resolved |
| T5 | Holes in the single source | Superseded by C2, C3, X1, X2 and X6 | resolved |
| T6 | `flushSync` in an effect | An ordinary state update, and a wait until visible (7.6) | resolved |
| T7 | A selector built from user text | `dataset` comparison (7.6) | resolved |
| T8 | Re-renders, normalization, test time, stub routes | A store, normalization at build, measured and reported, the stub route list | resolved |
| T9 | Site publication; where the reduced-motion rule goes | Plan Task 11; section 11 | resolved |
| T10 | The diff size | No move. The checks and the description line stay (C8). | resolved |
| U1 | Keep the query | 7.2 | resolved |
| U2 | Three tiers | 6.3 | resolved |
| U3 | Touch focus | 7.6 | resolved |
| U4 | Why the content column | 7.1 | resolved |
| U5 | Status debounce; option names; description | 7.10, 7.5 | resolved |
| U6 | Strings | 7.11, plan section 5 | resolved |
| U7 | Hidden drafts | Kept while searching; a jump loses them as today; separate issue | resolved |
| U8 | Docs assets | Plan section 6 | resolved |
| U9 | One acceptance list | Plan sections 0.1 and 8 | resolved |
| U10 | A duplicate row name | Superseded by C5 | resolved |
| C1 | Real boxes, named fallbacks, browser verification | Real `.settings-field` boxes (8.2); named focus targets (7.6); browser assertions of the ring, the bounds and the focus (13). Finished with X1 (no box for an entry frame) and X4 (executable recipes). Completed by Y1 and Y6. Completed by Z2. | resolved |
| C2 | The coverage guarantees; rows | An exact multiset of boxes, names and descriptions per box, with the mutation trio (8.3); rows named by `aria-label`. Finished with X1 and X2. Completed by Y2 and Y3. | resolved |
| C3 | Drawn text diverges from the index | `overrideTexts` resolved and consumed by the drawing (X6); `settingsTexts.ts`; `notificationsShown()`; a Telegram run | resolved |
| C4 | Portals outlive the pause | A full sweep table of portals and listeners (7.4), with `FieldHint` and `SessionTagEditor` handled (X3). Completed by Y4: one chokepoint per kind (`PanePortal`, `useEscapeCloses`) and a behavioural test over every layer, replacing the static test and FieldHint's own close. Completed by Z1. | resolved |
| C5 | Duplicate-row navigation | Rows sharing a name are not indexed (5.4) | resolved |
| C6 | Choosing the same result again; narrow `/` | A choice sequence; focus after commit (7.2, 7.3) | resolved |
| C7 | End-to-end surfaces; the URL | The relay, a Telegram touch scenario, the chat edge, WebKit and Chromium, `/?lang=` (13). Recipes made executable by X4. Completed by Y5. | resolved |
| C8 | Cut duplication, not acceptance | Existing scripts extended, one shared helper, measured | resolved |
| C9 | Qualify the cache claim | Section 12, corrected by X7 | resolved |
| X1 | Entry frames collide with the list's anchor | An entry frame gets no box; repeated child boxes; multiset comparison; empty and two-entry cases (5.1, 8.2, 8.3, 9). Completed by Y1 (the first box in document order, unfolded before the wait) and Y2 (counts read from the drawing, not stored). | resolved |
| X2 | Landmark collection takes in operational controls | A landmark is its heading plus the heading's (i), and nothing below (5.1, 8.3); stale-entry and changed-label mutations added | resolved |
| X3 | `SessionTagEditor` escapes the pause | Handled, with a full sweep table of portals and listeners in Settings' import closure (7.4). Completed by Y4: the static test is replaced by the chokepoints and a behavioural test. Completed by Z1. | resolved |
| X4 | Browser recipes cannot run as written | Focus moved out before `/`; a Telegram touch context with the media query asserted; results reopened before Back; tab-to-tiles Back as its own step; manual runs with prerequisites, since CI does not run these scripts (13, plan Task 9) Completed by Y5 and Y6. Completed by Z2. | resolved |
| X5 | Structural tests and test placement | `blocks()` unwraps `.settings-field`; drawer cases in `Settings.test.tsx`, comparison in `SettingsSection.test.tsx`, a shared fake extracted from `Settings.test.tsx` (13) | resolved |
| X6 | Who owns the override texts | The drawing consumes the resolved descriptor (8.1). The primary name is the title and the focus name (7.5, 7.6). The Proxy URL focus requirement is kept. | resolved |
| X7 | When an edited rule reaches a session | Rewritten: a rules generation rediscovers the catalog, and a scoped rule reaches history on activation (12) | resolved |
| Y1 | A folded target could deadlock the reveal | The mounted box is found first, its folds open, then the drawer waits for visibility (7.6). A folded entry is not an empty list. | resolved |
| Y2 | Expected box counts go stale when a list is edited | The index carries no count. The comparison reads each list's count from the drawing, per actual parent entry (8.3). An edit-after-index regression is added. | resolved |
| Y3 | Primary-first comparison conflicts with the markup's order | Names and descriptions are compared as multisets. The primary title and the focused control are asserted separately. The markup keeps its order (8.3). | resolved |
| Y4 | The static sweep test did not establish its guarantee | Replaced by two chokepoints, `PanePortal` and `useEscapeCloses`, plus a behavioural test over every layer in the sweep table. The narrower guarantee is stated (7.4). The sweep now lists FieldHint's `touchstart` (`:102`). Completed by Z1. | resolved |
| Y5 | Browser recipes are still non-executable | Queries are config keys; results are picked by anchor; expected names are read from the page. On a narrow shell, the script goes back to the tiles before reopening (13). | resolved |
| Y6 | The shared helper's contract | It polls for the class and the computed ring, checks the viewport and scroll area intersection, and checks exact focus identity (13). Completed by Z2. | resolved |
| Y7 | The Russian empty state asks for shorter words | It now asks for fewer words (plan section 5). | resolved |
| Z1 | A hidden pinned tip was closed by scrolling the results | `FieldHint` pauses its close listeners while its pane is hidden and is placed again when shown; the behavioural test scrolls the results and checks the tip is still pinned and placed (7.4) | resolved |
| Z2 | The helper read bounds before a smooth scroll finished | The ring, the bounds and the focus identity are polled together until all hold, within the deadline (13) | resolved |
| Z3 | The Russian greps cannot be empty | The plan requires no new matches: the greps list only the policy and check files `origin/main` already has, and the diff adds none (plan Task 7) | resolved |
