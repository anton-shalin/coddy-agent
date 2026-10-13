#!/usr/bin/env node
/**
 * Browser checks of the Settings drawer, two scenarios chosen by argument:
 *
 *   unsaved (the default)  issue #485: while the form holds an edit that is
 *                          not saved, Save stands out and the line before the
 *                          buttons says so;
 *   search                 the Settings search: a query finds a setting, the
 *                          drawer takes the reader to it (ring, bounds, focus),
 *                          on an agent's page and on a relay's own page.
 *
 * This is what the vitest suite cannot answer: jsdom has no layout, so it
 * cannot tell whether the unsaved line fits the footer beside the buttons on a
 * phone without pushing them off, whether a tap still reaches Save, or whether
 * a setting the search took you to is in sight, ringed and holding the cursor.
 *
 * It drives a real `coddy serve` with the web UI. The unsaved scenario edits
 * the form and puts the value back without saving; the search scenario only
 * types into the search field and picks results. Neither writes anything;
 * still, point them at a throwaway server with its own CODDY_HOME rather than
 * one you use.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_SERVE_URL=http://127.0.0.1:18485 npm run check:settings-unsaved
 *   CODDY_BIN=../../build/coddy npm run check:settings-search
 *
 * The search scenario drives two stands, each with its own HOME and
 * CODDY_HOME. With CODDY_BIN set and CODDY_SERVE_URL not, it starts them
 * itself from that binary (`TAGS="http ui swarm"`; settings-search-stands.mjs:
 * an agent with one `openai` provider `demo`, no key and three logical models,
 * and a relay), on free ports, and stops them at exit, failures included. With
 * CODDY_SERVE_URL it uses stands of yours instead:
 *   an agent  CODDY_SERVE_URL: `coddy serve` with one `openai` provider named
 *             `demo` (no key; no model is called) and two or more entries
 *             under `models:`;
 *   a relay   CODDY_RELAY_URL: `coddy serve` with `swarm.enable: true`, which
 *             asks for its client token even on loopback (CODDY_RELAY_TOKEN).
 * and fails without CODDY_RELAY_URL.
 *
 * CODDY_ENGINE=webkit (or firefox) runs the same checks in another engine;
 * CODDY_BROWSER_PATH points chromium at an installed browser (for example
 * /usr/bin/chromium) instead of Playwright's own download;
 * CODDY_AUTH_TOKEN is sent as the bearer token of an agent that asks for one.
 */

import fs from "node:fs";
import path from "node:path";

import {
  assertAddress,
  assertChosenSelected,
  assertDrawerState,
  assertGroupOpen,
  assertHighlight,
  assertKbd,
  assertNoTextControlFocused,
  assertQueryKept,
  assertRevealed,
  assertRowForm,
  assertTabOnly,
  assertTiers,
  assertTouchOnly,
  backToTiles,
  blurActive,
  openResult,
  openSearch,
  pressField,
  pressKey,
  readGroup,
  searchFor,
  shownResults,
  typeQuery,
  SEARCH_INPUT,
  TILE_GRID,
} from "./settings-search-assert.mjs";
import { startStands } from "./settings-search-stands.mjs";

const SCENARIOS = ["unsaved", "search"];
const SCENARIO = process.argv[2] || "unsaved";
if (!SCENARIOS.includes(SCENARIO)) {
  console.error(
    `unknown scenario ${SCENARIO} (use ${SCENARIOS.join(" or ")}; none means unsaved)`,
  );
  process.exit(2);
}

let BASE = (process.env.CODDY_SERVE_URL || "http://127.0.0.1:18485").replace(
  /\/+$/,
  "",
);
let RELAY = (process.env.CODDY_RELAY_URL || "").replace(/\/+$/, "");
let RELAY_TOKEN = process.env.CODDY_RELAY_TOKEN || "";
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const TOKEN = process.env.CODDY_AUTH_TOKEN || "";
const KEEP = process.env.CODDY_E2E_KEEP === "1";

// The search scenario starts its own stands from CODDY_BIN unless it is told
// where the stands are.
let stands = null;
if (
  SCENARIO === "search" &&
  process.env.CODDY_BIN &&
  !process.env.CODDY_SERVE_URL
) {
  const bin = path.resolve(process.env.CODDY_BIN);
  if (!fs.existsSync(bin)) {
    console.error(
      `${bin} not found; build it with: make build TAGS="http ui swarm"`,
    );
    process.exit(2);
  }
  process.on("SIGINT", () => process.exit(130));
  process.on("SIGTERM", () => process.exit(143));
  try {
    stands = await startStands(bin);
  } catch (err) {
    console.error(String(err.message || err));
    process.exit(2);
  }
  BASE = stands.agentUrl;
  RELAY = stands.relayUrl;
  RELAY_TOKEN = stands.relayToken;
  console.log(`stands from ${bin}: agent ${BASE}, relay ${RELAY}`);
}

if (SCENARIO === "search" && !RELAY) {
  console.error(
    "the search scenario needs stands: CODDY_BIN (it starts them), or CODDY_SERVE_URL and CODDY_RELAY_URL (a coddy serve with swarm.enable, for the relay's own Settings; CODDY_RELAY_TOKEN is its client token)",
  );
  process.exit(2);
}

// Both shipped locales, since the line's words are of a different length in
// each; a wide window, and phones held both ways, the smallest ones included.
const CASES = [
  { lang: "en", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 390, height: 844, touch: true },
  { lang: "ru", width: 360, height: 640, touch: true },
  { lang: "en", width: 320, height: 568, touch: true },
  { lang: "ru", width: 844, height: 390, touch: true },
  { lang: "ru", width: 568, height: 320, touch: true },
];

let playwright;
try {
  playwright = await import("playwright");
} catch {
  console.error(
    "playwright is not installed. Run: npm i --no-save playwright && npx playwright install chromium",
  );
  process.exit(2);
}
const launcher = playwright[ENGINE];
if (!launcher) {
  console.error(
    `unknown CODDY_ENGINE ${ENGINE} (use chromium, webkit or firefox)`,
  );
  process.exit(2);
}

const failures = [];
const fail = (where, what) => {
  failures.push(`${where}: ${what}`);
  console.log(`  FAIL ${what}`);
};
const headers = TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {};

/** The context of one case: the language, the window and the input device. */
function contextOptions(c, extraHTTPHeaders) {
  return {
    viewport: { width: c.width, height: c.height },
    locale: c.lang === "ru" ? "ru-RU" : "en-US",
    hasTouch: c.touch,
    isMobile: c.touch && ENGINE !== "firefox",
    extraHTTPHeaders,
  };
}

// ----------------------------------------------------------------- unsaved

async function runUnsaved(browser) {
  for (const c of CASES) {
    const where = `${c.lang} ${c.width}x${c.height}${c.touch ? " touch" : ""}`;
    console.log(where);
    const ctx = await browser.newContext(contextOptions(c, headers));
    const page = await ctx.newPage();
    await page.goto(`${BASE}/?lang=${c.lang}#/settings/agent`);
    // Max turns, the first number of the ReAct loop tab; its label is
    // translated, so it is found by its place.
    const input = page.locator('.settings-body input[type="number"]').first();
    await input.waitFor();
    const before = await input.inputValue();
    await input.fill(String(Number(before || "40") + 1));
    await page.evaluate(() => document.activeElement?.blur());
    const m = await page.evaluate(() => {
      const rect = (el) => el.getBoundingClientRect();
      const save = document.querySelector('[data-testid="settings-save"]');
      const status = document.querySelector(
        '[data-testid="settings-save-status"]',
      );
      const reload = document.querySelector('[data-testid="settings-reload"]');
      const b = rect(save);
      const at = document.elementFromPoint(
        b.left + b.width / 2,
        b.top + b.height / 2,
      );
      const s = rect(status);
      const r = rect(reload);
      return {
        scroll:
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
        dirty: save.classList.contains("is-dirty"),
        text: status.textContent,
        saveInside:
          b.left >= 0 &&
          b.top >= 0 &&
          b.right <= innerWidth &&
          b.bottom <= innerHeight,
        saveHit: save.contains(at),
        statusClear: s.right <= r.left + 0.5 && s.width > 0,
        statusOneRow:
          Math.abs(s.top + s.height / 2 - (b.top + b.height / 2)) < 6,
      };
    });
    if (m.scroll > 0) fail(where, `the page scrolls sideways by ${m.scroll}px`);
    if (!m.dirty) fail(where, "Save is not highlighted over an unsaved edit");
    if (!m.text) fail(where, "the line before the buttons says nothing");
    if (!m.saveInside) fail(where, "Save is outside the window");
    if (!m.saveHit)
      fail(where, "a tap at the middle of Save lands on something else");
    if (!m.statusClear) fail(where, "the status line runs into the buttons");
    if (!m.statusOneRow)
      fail(where, "the status line is not on the buttons' row");
    // Put the value back: the highlight goes, and nothing is saved.
    await input.fill(before);
    await page.evaluate(() => document.activeElement?.blur());
    if (
      await page
        .getByTestId("settings-save")
        .evaluate((e) => e.classList.contains("is-dirty"))
    ) {
      fail(where, "a value put back still highlights Save");
    }
    await ctx.close();
  }
}

// ------------------------------------------------------------------ search

/** Thrown after a step failed and was reported, to leave the rest of its case. */
class CaseAborted extends Error {}

/** One step of a case: what a helper throws is a failure of that step. */
async function step(where, label, fn) {
  try {
    const result = await fn();
    console.log(`  ok   ${label}`);
    return result;
  } catch (err) {
    fail(where, `${label}: ${err.message}`);
    throw new CaseAborted(label);
  }
}

/** A measured fact: reported either way, and the case goes on. */
function ok(where, label, holds, detail = "") {
  if (holds) {
    console.log(`  ok   ${label}`);
  } else {
    fail(where, `${label}${detail ? ` (${detail})` : ""}`);
  }
}

/**
 * Where the results are drawn and whether anything sticks out sideways. The
 * list itself may be taller than what is on screen: its scroll area, the part
 * the reader sees, is what has to sit inside the drawer.
 */
function readResultsLayout() {
  const drawer = document.querySelector('[data-testid="settings-screen"]');
  const input = document.querySelector(".settings-search-input");
  const id = input && input.getAttribute("aria-controls");
  const list =
    (id && document.getElementById(id)) ||
    document.querySelector('[role="listbox"]');
  if (!drawer || !list) {
    return { missing: !drawer ? "the drawer" : "the results" };
  }
  const area = list.closest(".settings-scroll") || list;
  const d = drawer.getBoundingClientRect();
  const a = area.getBoundingClientRect();
  const options = [...list.querySelectorAll('button[role="option"]')];
  const rounded = (r) =>
    `${Math.round(r.left)},${Math.round(r.top)} ${Math.round(r.right)},${Math.round(r.bottom)}`;
  return {
    count: options.length,
    inside:
      a.width > 0 &&
      a.left >= d.left - 1 &&
      a.right <= d.right + 1 &&
      a.top >= d.top - 1 &&
      a.bottom <= d.bottom + 1,
    stickOut: options.filter(
      (o) =>
        o.getBoundingClientRect().right > a.right + 1 ||
        o.getBoundingClientRect().left < a.left - 1,
    ).length,
    page:
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
    list: Math.max(
      list.scrollWidth - list.clientWidth,
      area.scrollWidth - area.clientWidth,
    ),
    drawer: drawer.scrollWidth - drawer.clientWidth,
    rect: `${rounded(a)} in ${rounded(d)}`,
  };
}

/** The row's form is on screen and its folded block (Advanced settings) is open. */
function rowFormWithAdvancedOpen(row) {
  const form = [
    ...document.querySelectorAll(".settings-detail[data-settings-row]"),
  ].find((e) => e.dataset.settingsRow === row);
  if (!form || form.getClientRects().length === 0) return false;
  const advanced = [...form.querySelectorAll("[data-settings-anchor]")].find(
    (e) => e.dataset.settingsAnchor === "group:providers:advanced",
  );
  return !!advanced && advanced.classList.contains("is-open");
}

/** An agent's page: the search, the reveal and the life of the query. */
async function searchOnAgent(browser, c, where) {
  const ctx = await browser.newContext(contextOptions(c, headers));
  ctx.setDefaultTimeout(10000);
  const page = await ctx.newPage();
  page.on("pageerror", (err) => fail(where, `page error: ${err.message}`));
  // Where the cursor goes on a reveal: a touch screen focuses the box, so no
  // keyboard rises; a mouse focuses the control named like the setting.
  const focus = c.touch ? "box" : "control";
  const focusWord = c.touch ? "box" : "control";
  try {
    await page.goto(`${BASE}/?lang=${c.lang}#/settings`);
    await step(where, "the drawer is drawn", () =>
      page
        .locator(
          '[data-testid="settings-tab-appearance"], [data-testid="settings-tile-appearance"]',
        )
        .first()
        .waitFor(),
    );
    await step(
      where,
      `the input is ${c.touch ? "touch only" : "a mouse"}`,
      () => assertTouchOnly(page, c.touch),
    );
    const narrow = await page.evaluate(
      () => matchMedia("(max-width: 1199px)").matches,
    );
    ok(
      where,
      "the shell is the narrow one below 1200px",
      narrow === c.width < 1200,
      `narrow ${narrow}`,
    );
    await step(
      where,
      c.touch
        ? 'the "/" badge is not drawn on a touch screen'
        : 'the empty field shows the "/" badge',
      () =>
        assertKbd(
          page,
          !c.touch,
          c.touch
            ? 'A touch-only device has no "/" key, so the badge should not be drawn.'
            : 'An empty field that does not hold the cursor should show the "/" badge.',
        ),
    );

    // 1. The field, a query that is a config key, the results inside the drawer.
    await step(where, "open the search and look for max_turns", async () => {
      await openSearch(page, { narrow });
      await searchFor(page, "max_turns");
    });
    await step(
      where,
      'the "/" badge is gone while the field holds a query',
      () =>
        assertKbd(
          page,
          false,
          "The badge should be gone while the field holds the cursor and a query.",
        ),
    );
    const layout = await page.evaluate(readResultsLayout);
    ok(where, "the results are drawn", !layout.missing, layout.missing);
    if (!layout.missing) {
      ok(
        where,
        "the results sit inside the drawer",
        layout.inside,
        layout.rect,
      );
      ok(
        where,
        "no result sticks out of the results area",
        layout.stickOut === 0,
        `${layout.stickOut} do`,
      );
      ok(
        where,
        "nothing scrolls sideways",
        layout.page <= 0 && layout.list <= 0 && layout.drawer <= 0,
        `page ${layout.page}px, results ${layout.list}px, drawer ${layout.drawer}px`,
      );
    }

    // 2. A setting of a tab: the tab opens and the setting is revealed.
    const turns = await step(where, "pick agent:max_turns", () =>
      openResult(page, { anchor: "agent:max_turns" }),
    );
    await step(
      where,
      `agent:max_turns is revealed (${focusWord} focused)`,
      () =>
        assertRevealed(page, {
          anchor: "agent:max_turns",
          focus,
          name: turns,
        }),
    );
    const agentHash = await page.evaluate(() => location.hash);
    ok(
      where,
      "the ReAct loop tab is active",
      agentHash.startsWith("#/settings/agent"),
      agentHash,
    );
    await step(where, "the address names agent:max_turns", () =>
      assertAddress(
        page,
        { field: "agent:max_turns", id: null },
        "Choosing a setting should write its anchor into the address as field=.",
      ),
    );

    // 3. Back to the field. A narrow shell goes through the head's arrow
    // first; a wide one blurs the input and presses "/".
    await step(where, "reopen the search", () => openSearch(page, { narrow }));

    // 4. A row of a list: the provider's proxy, its folded block unfolded. The
    // results of the words found in a name come before those found in a
    // description.
    await step(where, 'look for "proxy"', () => searchFor(page, "proxy"));
    await step(
      where,
      "results that match by name come before those that match by description",
      () => assertTiers(page, "proxy"),
    );
    const proxy = await step(
      where,
      "pick providers:proxy of the row demo",
      () => openResult(page, { anchor: "providers:proxy", row: "demo" }),
    );
    await step(
      where,
      "the row demo is open with Advanced settings unfolded",
      async () => {
        try {
          await page.waitForFunction(rowFormWithAdvancedOpen, "demo", {
            timeout: 3000,
          });
        } catch {
          throw new Error(
            'no .settings-detail[data-settings-row="demo"] with an open group:providers:advanced',
          );
        }
      },
    );
    await step(
      where,
      `providers:proxy of demo is revealed (${focusWord} focused)`,
      () =>
        assertRevealed(page, {
          anchor: "providers:proxy",
          row: "demo",
          focus,
          name: proxy,
        }),
    );
    await step(
      where,
      "the address names providers:proxy and the row demo",
      () =>
        assertAddress(
          page,
          { field: "providers:proxy", id: "demo" },
          "Choosing a setting of a row should write the row as id= and the anchor as field= into the address.",
        ),
    );
    if (c.touch) {
      await step(where, "no text control holds the cursor", () =>
        assertNoTextControlFocused(page),
      );
    } else {
      const held = await page.evaluate(() => {
        const a = document.activeElement;
        return {
          tag: a ? a.tagName : "",
          role: a ? a.getAttribute("role") : "",
          type: a ? a.getAttribute("type") : "",
        };
      });
      ok(
        where,
        "the cursor is in the URL input, not on the switch before it",
        held.tag === "INPUT" &&
          held.role !== "switch" &&
          held.type !== "checkbox",
        JSON.stringify(held),
      );
    }

    // 5. The query is kept: the field still holds it with the results hidden
    // (on the narrow shell the field is drawn over the tiles, so back to them
    // first), and a press of "/" or a tap on the field brings the results back
    // with the chosen one selected.
    await step(
      where,
      'the field keeps "proxy" and the results are hidden',
      async () => {
        if (narrow) await backToTiles(page);
        await assertQueryKept(page, "proxy");
      },
    );
    await step(
      where,
      narrow
        ? "a tap on the field brings the results back with the chosen one selected"
        : 'a press of "/" brings the results back with the chosen one selected',
      async () => {
        await openSearch(page, { narrow });
        await assertChosenSelected(page, {
          anchor: "providers:proxy",
          row: "demo",
        });
      },
    );

    // 6. A setting of a model's row opens that row and not another.
    const rows = await step(where, "look for temperature", async () => {
      await openSearch(page, { narrow });
      await searchFor(page, "temperature");
      const found = (await shownResults(page))
        .filter((r) => r.anchor === "models:temperature")
        .map((r) => r.row);
      if (found.length < 2) {
        throw new Error(
          `The Logical models tab should offer Temperature once for each of the stand's models, but ${found.length} results carry models:temperature.`,
        );
      }
      return found;
    });
    const [first, second] = rows;
    const named = { anchor: "models:temperature" };
    for (const [row, again] of [
      [first, false],
      [second, true],
    ]) {
      if (again) {
        await step(
          where,
          `the results return with the temperature of ${first} chosen`,
          async () => {
            await openSearch(page, { narrow });
            await assertChosenSelected(page, { ...named, row: first });
          },
        );
      }
      const temperature = await step(
        where,
        `pick models:temperature of the row ${row}`,
        () => openResult(page, { ...named, row }),
      );
      await step(
        where,
        `models:temperature of ${row} is revealed (${focusWord} focused)`,
        () => assertRevealed(page, { ...named, row, focus, name: temperature }),
      );
      await step(
        where,
        `the form of ${row} is the one open, with the ring inside it`,
        () => assertRowForm(page, row),
      );
      await step(where, `the address names the row ${row}`, () =>
        assertAddress(
          page,
          { field: "models:temperature", id: row },
          "Choosing a setting of a model's row should write that row as id= and the anchor as field= into the address.",
        ),
      );
      if (c.touch) {
        await step(where, "no text control holds the cursor", () =>
          assertNoTextControlFocused(page),
        );
      }
    }
    // Leaving the revealed setting takes it out of the address: a rail click
    // on the wide drawer, the head's arrow and a tap on a tile on the narrow
    // shell.
    await step(
      where,
      narrow
        ? "the head's arrow and a tile tap take the setting out of the address"
        : "a click on a section of the rail takes the setting out of the address",
      async () => {
        if (narrow) {
          await backToTiles(page);
          await assertAddress(
            page,
            { field: null },
            "The head's back arrow should take the setting out of the address.",
          );
          await page.locator('[data-testid="settings-tile-agent"]').tap();
        } else {
          await page.locator('[data-testid="settings-tab-agent"]').click();
        }
        await assertAddress(
          page,
          { field: null, id: null },
          narrow
            ? "A tap on a tile should leave no setting in the address."
            : "A click on a section of the rail should take the setting and the row out of the address.",
        );
      },
    );

    // 7. The highlight, Enter, and an emptied field. A click in the field while
    // the results show leaves the highlighted result alone; Enter opens it;
    // emptying the field clears the search, and the result chosen before is
    // forgotten.
    // A query set in one event, as a paste sets it, over the old one: the
    // result chosen before (the second row's temperature) is not among the
    // new ones, so the first is highlighted.
    await step(
      where,
      "a query pasted over the old one highlights the first result",
      async () => {
        // At once: a pointer resting where the chosen result was must not
        // take the highlight when the new results come in under it.
        await openSearch(page, { narrow });
        await searchFor(page, "proxy");
        await assertHighlight(
          page,
          0,
          "A query pasted over the old one should move the highlight to the first result, since the result chosen before is not among the new ones.",
        );
      },
    );
    const entered = await step(
      where,
      "a click in the field keeps the highlighted result",
      async () => {
        await typeQuery(page, "proxy");
        await pressKey(page, "ArrowDown");
        await assertHighlight(
          page,
          1,
          "ArrowDown should move the highlight to the next result.",
        );
        await pressField(page);
        await assertHighlight(
          page,
          1,
          "A click in the field while the results show should keep the highlighted result.",
        );
        return (await shownResults(page))[1];
      },
    );
    const enteredRow = entered.row ?? undefined;
    await pressKey(page, "Enter");
    await step(
      where,
      `Enter opens the highlighted result and reveals ${entered.anchor}`,
      () =>
        assertRevealed(page, {
          anchor: entered.anchor,
          row: enteredRow,
          focus: c.touch ? "box" : "control-or-box",
          name: entered.name,
        }),
    );
    await step(
      where,
      "emptying the field clears the search and forgets the chosen result",
      async () => {
        await openSearch(page, { narrow });
        await assertChosenSelected(page, {
          anchor: entered.anchor,
          row: enteredRow,
        });
        await page.locator(SEARCH_INPUT).first().fill("");
        await assertDrawerState(
          page,
          { open: true, query: "", resultsShowing: false },
          "Emptying the field should clear the search and hide the results.",
        );
        await blurActive(page);
        await assertKbd(
          page,
          !c.touch,
          c.touch
            ? 'A touch-only device has no "/" key, so the badge should stay away from the emptied field.'
            : 'The "/" badge should come back once the field is empty and the cursor has left it.',
        );
        await searchFor(page, "proxy");
        await assertHighlight(
          page,
          0,
          "Once the search was cleared the result chosen before should be forgotten, so the first result is highlighted.",
        );
      },
    );

    // 8. The tab behind the results keeps its scroll position (the wide
    // drawer hides it under the results; the narrow shell mounts none).
    if (!narrow) {
      await step(
        where,
        "the tab behind the results keeps its scroll position",
        async () => {
          const tab = '.settings-scroll[data-settings-tab="tools"]';
          await page.locator('[data-testid="settings-tab-tools"]').click();
          await page.waitForSelector(tab, { state: "visible" });
          const set = await page.evaluate((sel) => {
            const el = document.querySelector(sel);
            const travel = el.scrollHeight - el.clientHeight;
            el.scrollTop = Math.min(travel, 300);
            return { top: el.scrollTop, travel };
          }, tab);
          if (set.top < 100) {
            throw new Error(
              `The Tools and permissions tab should be long enough to scroll, but it has ${set.travel}px of travel, so this check would prove nothing.`,
            );
          }
          await openSearch(page, { narrow: false });
          await searchFor(page, "proxy");
          await pressKey(page, "Escape");
          await assertDrawerState(
            page,
            { open: true, query: "", resultsShowing: false },
            "Escape should clear the search and bring the tab back.",
          );
          let top = -1;
          for (let i = 0; i < 20; i++) {
            top = await page.evaluate(
              (sel) => document.querySelector(sel)?.scrollTop ?? -1,
              tab,
            );
            if (Math.abs(top - set.top) <= 1) return;
            await new Promise((r) => setTimeout(r, 100));
          }
          throw new Error(
            `The tab behind the results should keep its scroll position, but it was scrolled to ${set.top}px and is at ${top}px after the search was cleared.`,
          );
        },
      );
    }

    // 9. Escape takes one step back at a time: the search first, wherever the
    // cursor is, then the drawer's own step (a row back to its list, a tab
    // back to the tiles), and last the drawer itself.
    await step(
      where,
      "open the row demo for Escape to step back from",
      async () => {
        await openSearch(page, { narrow });
        await searchFor(page, "proxy");
        const name = await openResult(page, {
          anchor: "providers:proxy",
          row: "demo",
        });
        await assertDrawerState(
          page,
          { open: true, rowOpen: "demo", resultsShowing: false },
          "Choosing the Proxy URL of the provider demo should open the row demo.",
        );
        // The reveal moves the cursor when it is done; Escape waits for that.
        await assertRevealed(page, {
          anchor: "providers:proxy",
          row: "demo",
          focus,
          name,
        });
      },
    );
    await step(
      where,
      "Escape with the results showing and the cursor outside the field clears the search",
      async () => {
        if (narrow) await backToTiles(page);
        await openSearch(page, { narrow });
        await blurActive(page);
        await assertDrawerState(
          page,
          { resultsShowing: true, fieldFocused: false },
          "The results should stay while the cursor is outside the field.",
        );
        await pressKey(page, "Escape");
        await assertDrawerState(
          page,
          {
            open: true,
            query: "",
            resultsShowing: false,
            ...(narrow ? { tiles: true } : { rowOpen: "demo" }),
          },
          narrow
            ? "Escape with the results showing should clear the query and bring the tiles back, with the drawer still open."
            : "Escape with the results showing and the cursor outside the field should clear the query and leave the drawer and its row open.",
        );
      },
    );
    if (!narrow) {
      await step(
        where,
        "Escape then steps back from the row to its list",
        async () => {
          await pressKey(page, "Escape");
          await assertDrawerState(
            page,
            { open: true, rowOpen: null },
            "With no search left, Escape should take the drawer's own step, from the row back to its list.",
          );
        },
      );
      await step(
        where,
        "Escape in the field clears a query whose results are hidden",
        async () => {
          await openSearch(page, { narrow: false });
          await searchFor(page, "proxy");
          // A click that leaves the cursor where it is: a section of the rail
          // hides the results and keeps the query.
          await page.evaluate(() =>
            document
              .querySelector('[data-testid="settings-tab-agent"]')
              .click(),
          );
          await assertDrawerState(
            page,
            {
              open: true,
              query: "proxy",
              resultsShowing: false,
              fieldFocused: true,
            },
            "A section of the rail should hide the results and keep the query, with the cursor still in the field.",
          );
          await pressKey(page, "Escape");
          await assertDrawerState(
            page,
            { open: true, query: "" },
            "Escape in the field should clear a query whose results are hidden, and leave the drawer open.",
          );
        },
      );
    } else {
      await step(
        where,
        "Escape steps back from a row to its list, then to the tiles",
        async () => {
          await searchFor(page, "proxy");
          const name = await openResult(page, {
            anchor: "providers:proxy",
            row: "demo",
          });
          await assertDrawerState(
            page,
            { rowOpen: "demo" },
            "Choosing the Proxy URL of the provider demo should open the row demo.",
          );
          await assertRevealed(page, {
            anchor: "providers:proxy",
            row: "demo",
            focus,
            name,
          });
          await pressKey(page, "Escape");
          await assertDrawerState(
            page,
            { open: true, rowOpen: null, tiles: false },
            "Escape should take the drawer's own step, from the row back to its list.",
          );
          await pressKey(page, "Escape");
          await assertDrawerState(
            page,
            { open: true, tiles: true },
            "Escape should take the drawer's own step, from the tab back to the tiles.",
          );
        },
      );
    }
    await step(
      where,
      "Escape with nothing left to step back from closes Settings",
      async () => {
        await pressKey(page, "Escape");
        await assertDrawerState(
          page,
          { open: false },
          "With nothing left to step back from, Escape should close Settings.",
        );
      },
    );

    // 10. An address that names a setting opens it. Each link is a page of its
    // own (a new document, with a query parameter of its own, so it is no
    // navigation inside one) and reveals the setting as a choice does: the
    // tab, the row and the folded block open, the ring, the cursor. An anchor
    // that is gone only opens its tab.
    const fresh = async (tag, hash) => {
      const p = await ctx.newPage();
      p.on("pageerror", (err) => fail(where, `page error: ${err.message}`));
      await p.goto(`${BASE}/?lang=${c.lang}&${tag}#${hash}`);
      await p.locator('[data-testid="settings-screen"]').waitFor();
      return p;
    };
    const links = [
      {
        label: "a link to the Proxy URL of the row demo",
        hash: "/settings/providers?id=demo&field=providers%3Aproxy",
        anchor: "providers:proxy",
        row: "demo",
        name: proxy,
      },
      {
        label: "a link to a field of an object tab",
        hash: "/settings/agent?field=agent%3Amax_turns",
        anchor: "agent:max_turns",
        name: turns,
      },
    ];
    for (const [i, link] of links.entries()) {
      await step(where, `${link.label} reveals the setting`, async () => {
        const p = await fresh(`link${i}`, link.hash);
        try {
          await assertRevealed(p, {
            anchor: link.anchor,
            row: link.row,
            focus,
            name: link.name,
          });
          if (link.row) {
            await p.waitForFunction(rowFormWithAdvancedOpen, link.row, {
              timeout: 3000,
            });
          }
          if (c.touch) await assertNoTextControlFocused(p);
        } finally {
          await p.close();
        }
      });
    }
    await step(
      where,
      "a link to a setting that is gone only opens its tab",
      async () => {
        const p = await fresh("stale", "/settings/agent?field=agent%3Agone");
        try {
          await assertTabOnly(
            p,
            "agent",
            "An address naming a setting that does not exist should only open its tab.",
          );
        } finally {
          await p.close();
        }
      },
    );

    // 11. A group is a result of its own: the tab (and the row) opens, a
    // folded group that is itself the target unfolds, and the group's
    // fieldset is ringed and holds the cursor. The legends are read from the
    // page. Each search starts on a page of its own, so the group stands as
    // the page draws it.
    for (const group of [
      {
        label: "the first group of the ReAct loop tab",
        hash: "/settings/agent",
        read: { prefix: "group:agent:" },
      },
      {
        label: "the folded group of the row demo",
        hash: "/settings/providers?id=demo",
        read: { prefix: "group:providers:", row: "demo", folded: true },
        row: "demo",
        folded: true,
      },
    ]) {
      const found = await step(
        where,
        `read the legend of ${group.label}`,
        async () => {
          const p = await fresh(`legend-${group.read.prefix}`, group.hash);
          try {
            return await readGroup(p, group.read);
          } finally {
            await p.close();
          }
        },
      );
      await step(
        where,
        `a search for "${found.legend}" opens ${group.label} and rings it`,
        async () => {
          const p = await fresh(`group-${group.read.prefix}`, "/settings");
          try {
            await openSearch(p, { narrow });
            await searchFor(p, found.legend);
            const name = await openResult(p, {
              anchor: found.anchor,
              row: group.row,
            });
            await assertRevealed(p, {
              anchor: found.anchor,
              row: group.row,
              focus: "box",
              name,
            });
            if (group.folded) {
              await assertGroupOpen(p, found.anchor, group.row);
            }
            if (c.touch) await assertNoTextControlFocused(p);
          } finally {
            await p.close();
          }
        },
      );
    }
  } finally {
    await ctx.close();
  }
}

/** The relay's own page: step 6 of Task 9. */
async function searchOnRelay(browser, c, where) {
  const ctx = await browser.newContext(contextOptions(c, undefined));
  ctx.setDefaultTimeout(10000);
  // A relay asks for its client token; the page takes it from the environment
  // it was told to talk to, which is itself.
  await ctx.addInitScript(
    ({ base, token }) => {
      try {
        localStorage.setItem(
          "coddy_env",
          JSON.stringify({ mode: "remote", baseUrl: base, token }),
        );
      } catch {
        // storage blocked: the page then asks for the token and the step fails
      }
    },
    { base: RELAY, token: RELAY_TOKEN },
  );
  const page = await ctx.newPage();
  page.on("pageerror", (err) => fail(where, `page error: ${err.message}`));
  try {
    await page.goto(`${RELAY}/?lang=${c.lang}#/settings`);
    await step(
      where,
      "the relay's drawer is drawn with its Swarm relay tab",
      () =>
        page
          .locator(
            '[data-testid="settings-tab-swarm"], [data-testid="settings-tile-swarm"]',
          )
          .first()
          .waitFor({ timeout: 15000 }),
    );
    await step(
      where,
      `the input is ${c.touch ? "touch only" : "a mouse"}`,
      () => assertTouchOnly(page, c.touch),
    );
    const narrow = await page.evaluate(
      () => matchMedia("(max-width: 1199px)").matches,
    );
    await step(where, 'look for "pairing"', async () => {
      await openSearch(page, { narrow });
      await searchFor(page, "pairing");
    });
    const pairing = await step(where, "pick swarm:pairing_tokens", () =>
      openResult(page, { anchor: "swarm:pairing_tokens" }),
    );
    await step(where, "swarm:pairing_tokens is revealed", () =>
      assertRevealed(page, {
        anchor: "swarm:pairing_tokens",
        // A list of values draws no control named like the list: the box
        // itself holds the cursor then, and always on a touch screen.
        focus: c.touch ? "box" : "control-or-box",
        name: pairing,
      }),
    );
    const hash = await page.evaluate(() => location.hash);
    ok(
      where,
      "the Swarm relay tab is active",
      hash.startsWith("#/settings/swarm"),
      hash,
    );
    await step(
      where,
      "a link to a setting of the relay's own page reveals it",
      async () => {
        const link = await ctx.newPage();
        link.on("pageerror", (err) =>
          fail(where, `page error: ${err.message}`),
        );
        try {
          await link.goto(
            `${RELAY}/?lang=${c.lang}&link#/settings/swarm?field=swarm%3Apairing_tokens`,
          );
          await link.locator('[data-testid="settings-screen"]').waitFor();
          await assertRevealed(link, {
            anchor: "swarm:pairing_tokens",
            focus: c.touch ? "box" : "control-or-box",
            name: pairing,
          });
        } finally {
          await link.close();
        }
      },
    );
  } finally {
    await ctx.close();
  }
}

/**
 * The "/" badge on the narrow shell with a keyboard (a narrow desktop window):
 * the touch cases never show it, so this is where it is seen on a shell of
 * tiles.
 */
async function badgeOnNarrowKeyboard(browser) {
  const where = "en 390x844 keyboard";
  console.log(where);
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 },
    locale: "en-US",
    extraHTTPHeaders: headers,
  });
  ctx.setDefaultTimeout(10000);
  const page = await ctx.newPage();
  page.on("pageerror", (err) => fail(where, `page error: ${err.message}`));
  try {
    await page.goto(`${BASE}/?lang=en#/settings`);
    await step(where, "the tiles are drawn", () =>
      page.locator(TILE_GRID).waitFor(),
    );
    await step(where, "the input is a mouse", () =>
      assertTouchOnly(page, false),
    );
    await step(
      where,
      'the empty field shows the "/" badge over the tiles',
      () =>
        assertKbd(
          page,
          true,
          'An empty field that does not hold the cursor should show the "/" badge on the narrow shell too.',
        ),
    );
    await step(
      where,
      "the badge is gone while the field holds the cursor",
      async () => {
        await pressField(page);
        await assertKbd(
          page,
          false,
          "The badge should be gone while the field holds the cursor.",
        );
      },
    );
    await step(
      where,
      "the badge stays away while the field holds a query",
      async () => {
        await typeQuery(page, "proxy");
        await assertKbd(
          page,
          false,
          "The badge should be gone while the field holds a query.",
        );
      },
    );
    await step(
      where,
      "the badge comes back once the field is empty and left",
      async () => {
        await page.locator(SEARCH_INPUT).first().fill("");
        await blurActive(page);
        await assertKbd(
          page,
          true,
          'The "/" badge should come back once the field is empty and the cursor has left it.',
        );
      },
    );
  } finally {
    await ctx.close();
  }
}

/** Stops with a plain message when a stand is not what the scenario needs. */
async function preflight() {
  const get = (url, init) => fetch(url, init).catch(() => null);
  const agent = await get(`${BASE}/coddy/config/schema`, { headers });
  if (!agent || !agent.ok) {
    console.error(
      `the agent at ${BASE} does not serve /coddy/config/schema (status ${agent ? agent.status : "no answer"}); start it, or set CODDY_SERVE_URL${TOKEN ? "" : " and CODDY_AUTH_TOKEN if it asks for one"}`,
    );
    process.exit(2);
  }
  const info = await get(`${RELAY}/swarm/info`);
  const body = info && info.ok ? await info.json().catch(() => null) : null;
  if (!body || body.swarm !== true) {
    console.error(
      `${RELAY}/swarm/info does not answer as a relay; CODDY_RELAY_URL is a coddy serve with swarm.enable`,
    );
    process.exit(2);
  }
  const config = await get(`${RELAY}/coddy/config`, {
    headers: RELAY_TOKEN ? { Authorization: `Bearer ${RELAY_TOKEN}` } : {},
  });
  if (!config || !config.ok) {
    console.error(
      `the relay at ${RELAY} refused /coddy/config (status ${config ? config.status : "no answer"}); CODDY_RELAY_TOKEN is its client token`,
    );
    process.exit(2);
  }
}

async function runSearch(browser) {
  for (const c of CASES) {
    const where = `${c.lang} ${c.width}x${c.height}${c.touch ? " touch" : ""}`;
    console.log(where);
    for (const [page, run] of [
      ["", searchOnAgent],
      [" relay", searchOnRelay],
    ]) {
      try {
        await run(browser, c, where + page);
      } catch (err) {
        if (!(err instanceof CaseAborted)) {
          fail(where + page, err.stack || String(err));
        }
      }
    }
  }
  try {
    await badgeOnNarrowKeyboard(browser);
  } catch (err) {
    if (!(err instanceof CaseAborted)) {
      fail("en 390x844 keyboard", err.stack || String(err));
    }
  }
}

if (SCENARIO === "search") {
  await preflight();
}
const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
console.log(`engine ${ENGINE}, scenario ${SCENARIO}`);
if (SCENARIO === "search") {
  await runSearch(browser);
} else {
  await runUnsaved(browser);
}

await browser.close();
if (stands) stands.stop({ keepLogs: KEEP || failures.length > 0 });
if (failures.length > 0) {
  console.error(
    `\n${failures.length} failure(s):\n${failures.join("\n")}${stands ? `\nstand logs in ${stands.logs}` : ""}`,
  );
  process.exit(1);
}
console.log(
  SCENARIO === "search"
    ? "\nsettings search: all checks passed"
    : "\nsettings unsaved changes: all checks passed",
);
