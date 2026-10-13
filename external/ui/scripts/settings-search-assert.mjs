/**
 * The assertions of the Settings search that two browser checks share:
 * `settings-unsaved-check.mjs search` (the drawer on a page of its own, in
 * both languages and at every width) and `telegram-mini-app-check.mjs`'s
 * `settings` scenario (the same drawer inside a Mini App's frame).
 *
 * This is what the vitest suite cannot answer: jsdom has no layout, no paint
 * and no real focus, so whether a revealed setting is ringed, in sight and
 * holds the cursor is a fact of a real engine. Every function takes a
 * Playwright `Page` or `Frame` as its target and throws an `Error` that says
 * what did not hold; the caller decides whether that ends its run.
 *
 * Nothing here reads a label from a dictionary. A result is picked by its
 * anchor (`data-settings-target`, and `data-settings-row` for the row of a
 * list), and the name it must hand the focus to is the one the page drew on
 * that result, so the Russian and the English page are held to the same code.
 *
 * The DOM it relies on:
 *   .settings-search-input            the field (a combobox; aria-controls names the results)
 *   button[role=option]               a result, with data-settings-target and data-settings-row
 *   [data-settings-anchor=<anchor>]   the box of a setting (a row's form is .settings-detail[data-settings-row])
 *   .is-search-target                 on the box while its ring is painted
 *   .settings-head-back               the arrow of the narrow shell's head
 *   [data-testid=settings-tile-grid]  the narrow shell's tiles
 *   .settings-scroll                  what a box scrolls in
 */

export const SEARCH_INPUT = ".settings-search-input";
export const TILE_GRID = '[data-testid="settings-tile-grid"]';
export const HEAD_BACK = ".settings-head-back";

/** How long a reveal may take to hold: the wait for the box (1 s) and a smooth scroll. */
export const REVEAL_POLL_MS = 2000;
const LIST_WAIT_MS = 5000;

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** The page that owns a target: a frame knows it, a page is it. */
function pageOf(target) {
  return typeof target.page === "function" ? target.page() : target;
}

/** A tap where the browser has touch, a click where it has none. */
async function press(target, locator) {
  const touch = await target.evaluate(() => navigator.maxTouchPoints > 0);
  if (touch) {
    await locator.tap();
  } else {
    await locator.click();
  }
}

/**
 * The locator of the results' options, scoped to the list the field controls
 * when it says which (aria-controls), else to any listbox.
 */
async function optionsOf(target) {
  const id = await target
    .locator(SEARCH_INPUT)
    .first()
    .getAttribute("aria-controls")
    .catch(() => null);
  const scope = id ? `[id=${JSON.stringify(id)}]` : '[role="listbox"]';
  return target.locator(`${scope} button[role="option"]`);
}

/** A tap (touch) or a click (mouse) on the search field. */
export async function pressField(target) {
  await press(target, target.locator(SEARCH_INPUT).first());
}

/**
 * Asserts the touch-only media query is what the check believes it is, before
 * any step relies on it: a touch reveal focuses the box, so no keyboard rises,
 * and that is decided by this very query (`snapshotTouchOnly()` in the app).
 */
export async function assertTouchOnly(target, expected) {
  const got = await target.evaluate(
    () => matchMedia("(any-hover: none) and (any-pointer: coarse)").matches,
  );
  if (got !== expected) {
    throw new Error(
      `(any-hover: none) and (any-pointer: coarse) is ${got}, the check expects ${expected}`,
    );
  }
}

/**
 * What results the page shows now, in order:
 * `[{index, anchor, row, selected, hasDescription, name}]`, where `index` is
 * the result's place among the list's options, `hasDescription` says whether
 * it shows a description line (a word matched there) and `name` the primary
 * name it draws: its
 * first line, read in the page from what is drawn, without the markup of its
 * marks. A matched second name may follow it as " · <second>".
 */
export async function shownResults(target) {
  const options = await optionsOf(target);
  return options.evaluateAll((nodes) => {
    const norm = (text) => text.replace(/[\s\u00a0]+/g, " ").trim();
    return nodes
      .map((node, index) => {
        const title = node.querySelector(".settings-hit-title");
        const kids = [...node.children].filter((k) => k.textContent.trim());
        const first = title || (kids.length > 1 ? kids[0] : node);
        return {
          index,
          shown: node.getClientRects().length > 0,
          anchor: node.dataset.settingsTarget ?? "",
          row: node.dataset.settingsRow ?? null,
          selected: node.getAttribute("aria-selected") === "true",
          hasDescription: node.querySelector(".settings-hit-desc") !== null,
          // The title may carry " · <second name>" after the primary one.
          name: norm(first.innerText.split("\n").find((l) => l.trim()) || "")
            .split(" · ")[0]
            .trim(),
        };
      })
      .filter((r) => r.shown)
      .map(({ shown, ...result }) => result);
  });
}

/** True while at least one result is drawn. */
export async function resultsShowing(target) {
  return (await shownResults(target)).length > 0;
}

/**
 * The narrow shell goes back to its tiles through the head's arrow, more than
 * once when a row's form is open: that arrow leads to its list first. The
 * search field is not drawn over an open tab, only over the tiles.
 */
export async function backToTiles(target) {
  for (let hop = 0; hop < 4; hop++) {
    const back = target.locator(HEAD_BACK).first();
    if (!(await back.isVisible().catch(() => false))) break;
    const label = await back.getAttribute("aria-label");
    await press(target, back);
    await target
      .waitForFunction(
        ({ grid, headBack, previous }) => {
          const g = document.querySelector(grid);
          const b = document.querySelector(headBack);
          return (
            (g && g.getClientRects().length > 0) ||
            !b ||
            b.getAttribute("aria-label") !== previous
          );
        },
        { grid: TILE_GRID, headBack: HEAD_BACK, previous: label },
        { timeout: 5000 },
      )
      .catch(() => {
        throw new Error(
          "The head's arrow did not lead back: the tiles never came.",
        );
      });
  }
}

/**
 * Brings the cursor to the search field the way a person does.
 *
 * narrow: back to the tiles (`backToTiles`), then a tap on the field.
 * wide: the focused element is blurred, and `/` goes to a page with no text
 * field holding the cursor, never to one that has.
 */
export async function openSearch(target, { narrow }) {
  const input = target.locator(SEARCH_INPUT).first();
  if (narrow) {
    await backToTiles(target);
    await input.waitFor({ state: "visible", timeout: 5000 }).catch(() => {
      throw new Error("The search field is not drawn on the narrow shell.");
    });
    await press(target, input);
  } else {
    await target.evaluate(() => {
      const a = document.activeElement;
      if (a instanceof HTMLElement) a.blur();
    });
    const held = await target.evaluate(() => {
      const a = document.activeElement;
      const text =
        a &&
        (a.isContentEditable ||
          ["INPUT", "TEXTAREA", "SELECT"].includes(a.tagName));
      return text ? a.tagName.toLowerCase() : "";
    });
    if (held) {
      throw new Error(`a ${held} still holds the cursor: "/" would be typed`);
    }
    await pageOf(target).keyboard.press("/");
  }
  await target
    .waitForFunction(
      (sel) => document.activeElement === document.querySelector(sel),
      SEARCH_INPUT,
      { timeout: 3000 },
    )
    .catch(async () => {
      const at = await target.evaluate(() => {
        const a = document.activeElement;
        return a
          ? a.tagName.toLowerCase() +
              (typeof a.className === "string" && a.className.trim()
                ? "." + a.className.trim().split(/\s+/).join(".")
                : "")
          : "nothing";
      });
      throw new Error(
        `the search field did not take the cursor (${at} holds it)`,
      );
    });
}

/**
 * Types the query into the field and waits for the results: the field holds
 * exactly the query and at least one result is drawn. Returns how many are.
 */
export async function searchFor(target, query) {
  const input = target.locator(SEARCH_INPUT).first();
  await input.fill(query);
  const options = await optionsOf(target);
  const deadline = Date.now() + LIST_WAIT_MS;
  for (;;) {
    const value = await input.inputValue();
    const count = await options.count();
    if (value === query && count > 0) {
      // A frame for the results to settle on the query just typed.
      await target.evaluate(
        () =>
          new Promise((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(resolve)),
          ),
      );
      return options.count();
    }
    if (Date.now() > deadline) {
      throw new Error(
        `no results for "${query}" within ${LIST_WAIT_MS} ms (the field holds "${value}", ${count} results)`,
      );
    }
    await sleep(50);
  }
}

/**
 * Types the query key by key into an emptied field, as a person does, and
 * waits for the results (`searchFor` sets it in one event, as a paste does).
 * Emptying the field clears the search, so nothing chosen before is left.
 */
export async function typeQuery(target, query) {
  const input = target.locator(SEARCH_INPUT).first();
  await input.fill("");
  await input.pressSequentially(query, { delay: 15 });
  const options = await optionsOf(target);
  const deadline = Date.now() + LIST_WAIT_MS;
  for (;;) {
    const value = await input.inputValue();
    if (value === query && (await options.count()) > 0) return;
    if (Date.now() > deadline) {
      throw new Error(
        `No results for "${query}" typed key by key within ${LIST_WAIT_MS} ms (the field holds "${value}").`,
      );
    }
    await sleep(50);
  }
}

/**
 * Picks the result with this anchor (and this row, for a list's row) and
 * returns the primary name that result draws. Choosing hides the results,
 * keeps the query and starts the reveal, which `assertRevealed` then holds to
 * account.
 */
export async function openResult(target, { anchor, row }) {
  const named = `${anchor}${row == null ? "" : ` and row ${row}`}`;
  const deadline = Date.now() + LIST_WAIT_MS;
  let match = null;
  for (;;) {
    const shown = await shownResults(target).catch(() => []);
    const found = shown.filter(
      (r) => r.anchor === anchor && (row == null || r.row === row),
    );
    if (found.length > 1) {
      throw new Error(
        `${found.length} results carry the anchor ${named}: name the row`,
      );
    }
    if (found.length === 1) {
      match = found[0];
      break;
    }
    if (Date.now() > deadline) {
      throw new Error(
        `no result with anchor ${named} (shown: ${
          shown.map((r) => r.anchor + (r.row ? `@${r.row}` : "")).join(", ") ||
          "none"
        })`,
      );
    }
    await sleep(50);
  }
  const option = (await optionsOf(target)).nth(match.index);
  await option.scrollIntoViewIfNeeded();
  await press(target, option);
  return match.name;
}

/**
 * Everything one poll of a reveal reads, in the page. Each part is read again
 * on every poll, because a smooth scroll may still be running when the ring
 * appears: bounds read once and judged would pass or fail on a frame.
 */
function probeReveal({ anchor, row, focus, names }) {
  const norm = (s) => (s || "").replace(/[\s\u00a0]+/g, " ").trim();
  const describe = (el) =>
    !el
      ? "nothing"
      : `${el.tagName.toLowerCase()}${el.id ? "#" + el.id : ""}${
          el.className && typeof el.className === "string"
            ? "." + el.className.trim().split(/\s+/).join(".")
            : ""
        }${el.getAttribute("role") ? `[role=${el.getAttribute("role")}]` : ""}`;

  // The box: the first one in document order whose dataset equals the
  // strings, inside the row's form when a row is named (never a selector
  // built from the values).
  const roots =
    row === undefined || row === null
      ? [document]
      : [
          ...document.querySelectorAll(".settings-detail[data-settings-row]"),
        ].filter((e) => e.dataset.settingsRow === row);
  let box = null;
  for (const root of roots) {
    if (
      root instanceof HTMLElement &&
      root !== document.documentElement &&
      root.dataset.settingsAnchor === anchor
    ) {
      box = root;
      break;
    }
    for (const el of root.querySelectorAll("[data-settings-anchor]")) {
      if (el.dataset.settingsAnchor === anchor) {
        box = el;
        break;
      }
    }
    if (box) break;
  }
  const label = `${anchor}${row == null ? "" : `@${row}`}`;
  if (!box) {
    const missing = `no box with data-settings-anchor=${anchor}${row == null ? "" : ` in the form of row ${row}`}`;
    return {
      ring: { ok: false, why: missing },
      bounds: { ok: false, why: missing },
      focus: { ok: false, why: missing },
      label,
    };
  }

  // The ring is an outline over an inset wash (or, in a theme that paints it
  // so, a shadow): either one painted on the marked box counts.
  const cs = getComputedStyle(box);
  const marked = box.classList.contains("is-search-target");
  const outlined =
    cs.outlineStyle !== "none" && Number.parseFloat(cs.outlineWidth) > 0;
  const shadowed = cs.boxShadow !== "none";
  const ring = {
    ok: marked && (outlined || shadowed),
    why: `.is-search-target ${marked}, outline ${cs.outlineStyle} ${cs.outlineWidth}, box-shadow ${shadowed ? "painted" : "none"}`,
  };

  const rect = box.getBoundingClientRect();
  const scroller = box.closest(".settings-scroll");
  const visible = { left: 0, top: 0, right: innerWidth, bottom: innerHeight };
  if (scroller) {
    const s = scroller.getBoundingClientRect();
    visible.left = Math.max(visible.left, s.left);
    visible.top = Math.max(visible.top, s.top);
    visible.right = Math.min(visible.right, s.right);
    visible.bottom = Math.min(visible.bottom, s.bottom);
  }
  // The outline stands 3px off the box and is 2px wide: it is part of what
  // has to be in sight. A box taller than the room is aligned by its top,
  // 12px under the edge (scroll-margin-block), instead of centred.
  const slack = 1;
  const reach = 5;
  const margin = 12;
  const tall = rect.height + 2 * reach > visible.bottom - visible.top;
  const r = (n) => Math.round(n * 10) / 10;
  const bounds = {
    ok:
      scroller !== null &&
      rect.width > 0 &&
      rect.height > 0 &&
      rect.left - reach >= visible.left - slack &&
      rect.right + reach <= visible.right + slack &&
      (tall
        ? rect.top >= visible.top - slack &&
          rect.top <= visible.top + margin + reach + slack
        : rect.top - reach >= visible.top - slack &&
          rect.bottom + reach <= visible.bottom + slack),
    why: `box top ${r(rect.top)} bottom ${r(rect.bottom)} left ${r(rect.left)} right ${r(rect.right)}, visible part top ${r(visible.top)} bottom ${r(visible.bottom)} left ${r(visible.left)} right ${r(visible.right)}, ${tall ? "taller than the room: aligned by its top" : "the 5px of outline included"}${scroller ? "" : " (no .settings-scroll around the box)"}`,
  };

  // The accessible name of a control, from aria-labelledby, aria-label or its
  // <label>; the hint beside a label is no part of it.
  const textOf = (node) => {
    let out = "";
    const walk = (x) => {
      if (x.nodeType === 3) out += x.nodeValue;
      else if (
        x.nodeType === 1 &&
        !x.matches(".field-hint, [aria-hidden='true'], script, style")
      ) {
        x.childNodes.forEach(walk);
      }
    };
    walk(node);
    return norm(out);
  };
  const nameOf = (el) => {
    const by = el.getAttribute("aria-labelledby");
    if (by) {
      const t = norm(
        by
          .split(/\s+/)
          .map((id) => {
            const ref = document.getElementById(id);
            return ref ? textOf(ref) : "";
          })
          .join(" "),
      );
      if (t) return t;
    }
    const al = norm(el.getAttribute("aria-label"));
    if (al) return al;
    if (el.labels && el.labels.length) {
      const t = norm([...el.labels].map(textOf).join(" "));
      if (t) return t;
    }
    return "";
  };
  const controls = [
    ...box.querySelectorAll(
      "input, select, textarea, button, [role=switch], [role=combobox], [role=checkbox], [tabindex]",
    ),
  ].filter(
    (el) =>
      !el.matches(
        ".field-hint, [disabled], [aria-disabled='true'], input[type=hidden]",
      ),
  );
  const control = controls.find((el) => names.includes(nameOf(el))) ?? null;

  const active = document.activeElement;
  let expected = null;
  let focusOk = false;
  let note = "";
  if (focus === "box") {
    expected = box;
  } else if (focus === "control") {
    expected = control;
    if (!control) {
      note = `no enabled control named ${JSON.stringify(names[0])} in the box (named: ${
        controls
          .map(nameOf)
          .filter(Boolean)
          .map((n) => JSON.stringify(n))
          .join(", ") || "none"
      })`;
    }
  } else {
    // "control-or-box": the control when the box has one of that name, else the box.
    expected = control ?? box;
  }
  focusOk = expected !== null && active === expected;
  const focusState = {
    ok: focusOk,
    why: `${note ? note + "; " : ""}the cursor is in ${describe(active)}, expected ${describe(expected)}`,
  };

  return { ring, bounds, focus: focusState, label };
}

/**
 * Holds a reveal to account: within `REVEAL_POLL_MS` the box of the setting
 * must carry its ring (a class and a painted shadow), lie inside the part of
 * its scroll area that is on screen, and have the focus, all three at once.
 *
 * focus "control": the control inside the box whose accessible name is `name`
 *   (identity, in the page: a Proxy URL result focuses the URL input, never
 *   the Ignore-system-proxy switch drawn before it);
 * focus "box": the box itself, as on a touch screen, where no keyboard may rise;
 * focus "control-or-box": the control when the box has one of that name, else
 *   the box, as a list of values whose items carry no name of their own.
 *
 * `name` is what `openResult` returned. A result may append " · <second
 * name>" to its primary one, so the part before that is a name too.
 */
export async function assertRevealed(target, { anchor, row, focus, name }) {
  if (!["control", "box", "control-or-box"].includes(focus)) {
    throw new Error(
      `assertRevealed: focus must be control or box, not ${focus}`,
    );
  }
  const names = [name, String(name).split(" · ")[0]].filter(
    (n, i, all) => n && all.indexOf(n) === i,
  );
  const deadline = Date.now() + REVEAL_POLL_MS;
  // A part that held at one poll and not at the last was lost on the way (the
  // ring fades after 1.6 s), which is not the same failure as one that never
  // held, so the report says which.
  const held = { ring: false, bounds: false, focus: false };
  let last = null;
  for (;;) {
    last = await target.evaluate(probeReveal, { anchor, row, focus, names });
    for (const k of Object.keys(held)) held[k] ||= last[k].ok;
    if (last.ring.ok && last.bounds.ok && last.focus.ok) return;
    if (Date.now() > deadline) break;
    await sleep(60);
  }
  const failed = ["ring", "bounds", "focus"]
    .filter((k) => !last[k].ok)
    .map(
      (k) =>
        `${k}${held[k] ? " (held earlier, then lost)" : " (never held)"}: ${last[k].why}`,
    );
  throw new Error(
    `${last.label} was not revealed within ${REVEAL_POLL_MS} ms:\n    ${failed.join("\n    ")}`,
  );
}

// ------------------------------------------------- the life of the query

/** A key press on the page that owns the target. */
export async function pressKey(target, key) {
  await pageOf(target).keyboard.press(key);
}

/** Moves the cursor off whatever holds it, as a click on blank ground does. */
export async function blurActive(target) {
  await target.evaluate(() => {
    const a = document.activeElement;
    if (a instanceof HTMLElement) a.blur();
  });
}

/**
 * What the drawer looks like now, read in the page:
 * `open` the drawer is drawn; `query` what the field holds (null while the
 * field is not drawn, as over an open tab on the narrow shell);
 * `fieldFocused` the cursor is in the field; `resultsShowing` at least one
 * result is drawn; `rowOpen` the row of the row form on screen, or null;
 * `tiles` the narrow shell's tiles are on screen; `headBack` the label of the
 * head's arrow, or null.
 */
export async function drawerState(target) {
  return target.evaluate(() => {
    const q = (sel) => document.querySelector(sel);
    const drawn = (el) => !!el && el.getClientRects().length > 0;
    const input = q(".settings-search-input");
    const id = input && input.getAttribute("aria-controls");
    const list = id ? document.getElementById(id) : null;
    const options = list
      ? [...list.querySelectorAll('button[role="option"]')].filter(drawn)
      : [];
    const forms = [
      ...document.querySelectorAll(".settings-detail[data-settings-row]"),
    ].filter(drawn);
    const back = q(".settings-head-back");
    return {
      open: q('[data-testid="settings-screen"]') !== null,
      query: drawn(input) ? input.value : null,
      fieldFocused: !!input && document.activeElement === input,
      resultsShowing: options.length > 0,
      rowOpen: forms.length > 0 ? forms[0].dataset.settingsRow : null,
      tiles: drawn(q('[data-testid="settings-tile-grid"]')),
      headBack: drawn(back) ? back.getAttribute("aria-label") : null,
    };
  });
}

/**
 * Holds the drawer to what it should look like: every key of `expected` must
 * equal the one `drawerState` reads, within two seconds (a key press is
 * answered by a state update). `sentence` says what was expected and why, and
 * leads the message when it does not hold.
 */
export async function assertDrawerState(target, expected, sentence) {
  const deadline = Date.now() + 2000;
  let state = null;
  for (;;) {
    state = await drawerState(target);
    const wrong = Object.keys(expected).filter((k) => state[k] !== expected[k]);
    if (wrong.length === 0) return;
    if (Date.now() > deadline) {
      throw new Error(
        `${sentence} The drawer is ${JSON.stringify(state)}, but ${wrong
          .map((k) => `${k} should be ${JSON.stringify(expected[k])}`)
          .join(" and ")}.`,
      );
    }
    await sleep(60);
  }
}

/**
 * After a result was chosen the field still holds the query and the results
 * are hidden. (On the narrow shell the field is drawn over the tiles only:
 * call `backToTiles` first.)
 */
export async function assertQueryKept(target, query) {
  await assertDrawerState(
    target,
    { query, resultsShowing: false },
    `Choosing a result should hide the results and keep "${query}" in the field.`,
  );
}

/**
 * The results are back after a press of "/" or a tap on the field, and the
 * result chosen before is the highlighted one (aria-selected).
 */
export async function assertChosenSelected(target, { anchor, row }) {
  const named = `${anchor}${row == null ? "" : ` of ${row}`}`;
  const deadline = Date.now() + 3000;
  let shown = [];
  for (;;) {
    shown = await shownResults(target);
    const chosen = shown.find(
      (r) => r.anchor === anchor && (row == null || r.row === row),
    );
    if (chosen && chosen.selected) return;
    if (Date.now() > deadline) {
      throw new Error(
        chosen
          ? `The result chosen before (${named}) should be highlighted when the results come back, but it is shown without aria-selected.`
          : `The results should come back with the result chosen before (${named}), but it is not among the ${shown.length} shown.`,
      );
    }
    await sleep(60);
  }
}

/** The result at `index` is the highlighted one, and no other is. */
export async function assertHighlight(target, index, sentence) {
  const deadline = Date.now() + 2000;
  let shown = [];
  for (;;) {
    shown = await shownResults(target);
    const at = shown.findIndex((r) => r.selected);
    const count = shown.filter((r) => r.selected).length;
    if (at === index && count === 1) return;
    if (Date.now() > deadline) {
      throw new Error(
        `${sentence} Result ${index + 1} should be the highlighted one, but ${
          count === 0 ? "none is" : `result ${at + 1} is`
        }.`,
      );
    }
    await sleep(60);
  }
}

/**
 * Results order by where the words are found: every result whose own name
 * holds them comes before any result that shows a description line, so the
 * description matches form a suffix of the list. Both kinds must be present,
 * or the check proves nothing.
 */
export async function assertTiers(target, query) {
  const shown = await shownResults(target);
  const named = shown.filter((r) => !r.hasDescription).length;
  const described = shown.length - named;
  if (named === 0 || described === 0) {
    throw new Error(
      `A search for "${query}" should show results that match by name and results that match by description, but it shows ${named} of the first and ${described} of the second.`,
    );
  }
  const firstDescribed = shown.findIndex((r) => r.hasDescription);
  const late = shown.findIndex(
    (r, i) => i > firstDescribed && !r.hasDescription,
  );
  if (late !== -1) {
    throw new Error(
      `Results that match by name should come before those that match by description, but "${shown[late].name}" (result ${late + 1}) matches by name and follows "${shown[firstDescribed].name}" (result ${firstDescribed + 1}), which shows a description.`,
    );
  }
}

/**
 * On a touch screen a reveal must not raise the keyboard: no text control
 * holds the cursor afterwards (the box does, as `assertRevealed` checks).
 */
export async function assertNoTextControlFocused(target) {
  const held = await target.evaluate(() => {
    const a = document.activeElement;
    const text =
      a &&
      (a.isContentEditable ||
        ["INPUT", "TEXTAREA", "SELECT"].includes(a.tagName));
    return text
      ? `${a.tagName.toLowerCase()}${a.className ? "." + String(a.className).trim().split(/\s+/).join(".") : ""}`
      : "";
  });
  if (held) {
    throw new Error(
      `On a touch screen no text control should take the cursor after a reveal, since the keyboard would rise, but ${held} holds it.`,
    );
  }
}

/**
 * A result of a list's row opens that row: the row form on screen is the
 * option's own row, and every ring painted lies inside it, never in another
 * row's form.
 */
export async function assertRowForm(target, row) {
  const deadline = Date.now() + REVEAL_POLL_MS;
  let seen = null;
  for (;;) {
    seen = await target.evaluate((wanted) => {
      const drawn = (el) => el.getClientRects().length > 0;
      const forms = [
        ...document.querySelectorAll(".settings-detail[data-settings-row]"),
      ].filter(drawn);
      const rows = forms.map((f) => f.dataset.settingsRow);
      const rings = [...document.querySelectorAll(".is-search-target")];
      const strays = rings.filter((el) => {
        const form = el.closest(".settings-detail[data-settings-row]");
        return !form || form.dataset.settingsRow !== wanted;
      });
      return { rows, rings: rings.length, strays: strays.length, wanted };
    }, row);
    if (seen.rows.length === 1 && seen.rows[0] === row && seen.strays === 0) {
      return;
    }
    if (Date.now() > deadline) {
      throw new Error(
        `The result of the row ${row} should open that row's form and ring a setting inside it, but the forms on screen are ${JSON.stringify(seen.rows)} and ${seen.strays} of ${seen.rings} rings lie outside it.`,
      );
    }
    await sleep(60);
  }
}

// ------------------------------------- the address, groups and the badge

/**
 * The Settings address, read apart: `#/settings/<tab>?id=<row>&field=<anchor>`
 * is `{tab, id, field}`, each null when the address does not carry it.
 */
export async function addressOf(target) {
  return target.evaluate(() => {
    const [path, query = ""] = location.hash.replace(/^#/, "").split("?");
    const params = new URLSearchParams(query);
    const parts = path.split("/").filter(Boolean);
    return {
      hash: location.hash,
      tab: parts[0] === "settings" ? (parts[1] ?? null) : null,
      id: params.get("id"),
      field: params.get("field"),
    };
  });
}

/**
 * Holds the address to what a choice should leave in it: `field` names the
 * setting by its anchor (null: the address names none) and, when `id` is
 * given, `id` names the row (null: no row).
 */
export async function assertAddress(target, { field, id }, sentence) {
  const deadline = Date.now() + 2000;
  let seen = null;
  for (;;) {
    seen = await addressOf(target);
    if (seen.field === field && (id === undefined || seen.id === id)) return;
    if (Date.now() > deadline) {
      throw new Error(
        `${sentence} The address is ${seen.hash}, but field should be ${
          field === null ? "absent" : JSON.stringify(field)
        }${id === undefined ? "" : ` and id ${id === null ? "absent" : JSON.stringify(id)}`}.`,
      );
    }
    await sleep(60);
  }
}

/**
 * The tab stands open and no setting is ringed, for as long as a reveal would
 * take to show a ring (an address that names a setting which is gone only
 * opens its tab). Fails on a ring at any moment of the wait, and on an error
 * line the drawer draws.
 */
export async function assertTabOnly(target, tab, sentence) {
  const deadline = Date.now() + 1800;
  for (;;) {
    const seen = await target.evaluate(() => ({
      rings: document.querySelectorAll(".is-search-target").length,
      errors: [...document.querySelectorAll(".settings-error")].map((e) =>
        e.textContent.trim(),
      ),
    }));
    if (seen.rings > 0) {
      throw new Error(
        `${sentence} A setting is ringed, though none was found.`,
      );
    }
    if (seen.errors.length > 0) {
      throw new Error(
        `${sentence} The drawer shows an error: ${seen.errors[0]}`,
      );
    }
    if (Date.now() > deadline) break;
    await sleep(80);
  }
  const address = await addressOf(target);
  const drawn = await target.evaluate(
    (id) =>
      [
        ...document.querySelectorAll(".settings-scroll[data-settings-tab]"),
      ].some(
        (el) => el.dataset.settingsTab === id && el.getClientRects().length > 0,
      ) ||
      // the narrow shell mounts the open tab alone
      document.querySelector(".settings-mobile-detail") !== null,
    tab,
  );
  if (address.tab !== tab || !drawn) {
    throw new Error(
      `${sentence} The tab ${tab} should be open (address ${address.hash}), but ${drawn ? "the address names another" : "it is not drawn"}.`,
    );
  }
}

/**
 * The first group of a tab, or of a row's form, read from the page: its
 * anchor (`group:<domain>:<id>`) and the legend it draws. `folded` asks for
 * one that stands shut.
 */
export async function readGroup(target, { prefix, row, folded }) {
  const deadline = Date.now() + 5000;
  for (;;) {
    const found = await target.evaluate(
      ({ prefix, row, folded }) => {
        const norm = (s) => s.replace(/[\s\u00a0]+/g, " ").trim();
        const scopes =
          row == null
            ? [document]
            : [
                ...document.querySelectorAll(
                  ".settings-detail[data-settings-row]",
                ),
              ].filter((e) => e.dataset.settingsRow === row);
        for (const scope of scopes) {
          for (const box of scope.querySelectorAll("[data-settings-anchor]")) {
            const anchor = box.dataset.settingsAnchor;
            if (!anchor.startsWith(prefix)) continue;
            // A group that can fold carries --collapsible, and stands shut
            // until it is open.
            if (
              folded &&
              (!box.classList.contains("settings-fieldset--collapsible") ||
                box.classList.contains("is-open"))
            ) {
              continue;
            }
            const legend = box.querySelector("legend");
            if (!legend) continue;
            let text = "";
            const walk = (n) => {
              if (n.nodeType === 3) text += n.nodeValue;
              else if (
                n.nodeType === 1 &&
                !n.matches(".field-hint, [aria-hidden='true'], svg")
              ) {
                n.childNodes.forEach(walk);
              }
            };
            walk(legend);
            if (norm(text)) return { anchor, legend: norm(text) };
          }
        }
        return null;
      },
      { prefix, row, folded },
    );
    if (found) return found;
    if (Date.now() > deadline) {
      throw new Error(
        `No ${folded ? "folded " : ""}group with an anchor starting ${prefix}${row == null ? "" : ` in the form of ${row}`} is drawn, so there is no legend to search for.`,
      );
    }
    await sleep(100);
  }
}

/** A group the search opened is unfolded, whether it stood shut or not. */
export async function assertGroupOpen(target, anchor, row) {
  const deadline = Date.now() + 2000;
  for (;;) {
    const open = await target.evaluate(
      ({ anchor, row }) => {
        const scopes =
          row == null
            ? [document]
            : [
                ...document.querySelectorAll(
                  ".settings-detail[data-settings-row]",
                ),
              ].filter((e) => e.dataset.settingsRow === row);
        for (const scope of scopes) {
          for (const box of scope.querySelectorAll("[data-settings-anchor]")) {
            if (box.dataset.settingsAnchor === anchor) {
              return box.classList.contains("is-open");
            }
          }
        }
        return null;
      },
      { anchor, row },
    );
    if (open === true) return;
    if (Date.now() > deadline) {
      throw new Error(
        open === null
          ? `The group ${anchor} should be drawn after the search opened it, but it is not.`
          : `The group ${anchor} was the target of the search and should be unfolded, but it stands shut.`,
      );
    }
    await sleep(60);
  }
}

/**
 * The "/" badge in the search field: drawn while the field is empty and does
 * not hold the cursor, on every width; gone while it holds the cursor or a
 * query; never drawn on a touch-only device. `shown` is what is expected.
 */
export async function assertKbd(target, shown, sentence) {
  const deadline = Date.now() + 2000;
  let seen = null;
  for (;;) {
    seen = await target.evaluate((sel) => {
      const input = document.querySelector(sel);
      const kbd = document.querySelector(".settings-search-kbd");
      return {
        drawn: !!kbd && kbd.getClientRects().length > 0,
        text: kbd ? kbd.textContent.trim() : null,
        query: input ? input.value : null,
        focused: !!input && document.activeElement === input,
      };
    }, SEARCH_INPUT);
    if (seen.drawn === shown && (!shown || seen.text === "/")) return;
    if (Date.now() > deadline) {
      throw new Error(
        `${sentence} The badge is ${seen.drawn ? `drawn (${JSON.stringify(seen.text)})` : "not drawn"} with the field ${seen.query === "" ? "empty" : `holding ${JSON.stringify(seen.query)}`} and ${seen.focused ? "focused" : "not focused"}, but it should be ${shown ? 'drawn as "/"' : "gone"}.`,
      );
    }
    await sleep(60);
  }
}
